package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// writeIndexTestCohort writes a minimal single-file .pulse cohort with
// an "id" (u32) and "region" (categorical_u8) field directly to the
// real OS filesystem at path — newPulse() (used by IndexCommand's
// build leaf) wires up afero.NewOsFs() with no DataDir, so CLI-level
// tests exercise real files rather than an in-memory FS.
func writeIndexTestCohort(t *testing.T, path string) {
	t.Helper()

	dict := encoding.NewDictionary()
	dict.Add("north") // id 0
	dict.Add("south") // id 1

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: dict},
		},
	}
	records := [][]uint64{
		{1, 0}, // north
		{2, 1}, // south
		{3, 0}, // north (duplicate region key)
	}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, rec := range records {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				t.Fatalf("WriteFieldValue: %v", err)
			}
		}
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile cohort: %v", err)
	}
}

// runIndexCLI drives a fresh IndexCommand with the supplied args,
// capturing stdout into buf so --json assertions can decode it.
func runIndexCLI(t *testing.T, buf *bytes.Buffer, args ...string) error {
	t.Helper()
	root := IndexCommand()
	root.Writer = buf
	return root.Run(context.Background(), append([]string{"index"}, args...))
}

func TestIndexBuildCLI_SingleKeyWritesSidecar(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeIndexTestCohort(t, cohort)

	var buf bytes.Buffer
	if err := runIndexCLI(t, &buf, "build", "--input", cohort, "--key", "id"); err != nil {
		t.Fatalf("run: %v", err)
	}

	wantPath := encoding.SidecarIndexPath(cohort, []string{"id"})
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("sidecar not written at derived path %q: %v", wantPath, err)
	}
}

func TestIndexBuildCLI_CompositeKeyWritesSidecar(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeIndexTestCohort(t, cohort)

	var buf bytes.Buffer
	if err := runIndexCLI(t, &buf, "build", "--input", cohort, "--key", "region,id"); err != nil {
		t.Fatalf("run: %v", err)
	}

	wantPath := encoding.SidecarIndexPath(cohort, []string{"region", "id"})
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("composite sidecar not written at derived path %q: %v", wantPath, err)
	}
	// A single-key build over ["id"] must derive a DIFFERENT path than
	// the composite ["region","id"] build above — distinct key lists
	// (and key order) always derive distinct sidecar paths.
	singleKeyPath := encoding.SidecarIndexPath(cohort, []string{"id"})
	if singleKeyPath == wantPath {
		t.Errorf("composite key path collided with single-key path: %q", wantPath)
	}
}

func TestIndexBuildCLI_JSONEnvelopeShape(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeIndexTestCohort(t, cohort)

	var buf bytes.Buffer
	if err := runIndexCLI(t, &buf, "build", "--input", cohort, "--key", "id", "--json"); err != nil {
		t.Fatalf("run: %v", err)
	}

	var env struct {
		FormatVersion string `json:"format_version"`
		Data          struct {
			Cohort         string   `json:"cohort"`
			IndexPath      string   `json:"index_path"`
			Keys           []string `json:"keys"`
			DistinctKeys   int      `json:"distinct_keys"`
			IndexedRecords int      `json:"indexed_records"`
		} `json:"data"`
		Errors   []map[string]any `json:"errors"`
		Warnings []map[string]any `json:"warnings"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want %q", env.FormatVersion, "1.1")
	}
	if env.Errors == nil || len(env.Errors) != 0 {
		t.Errorf("errors = %v, want empty non-null array", env.Errors)
	}
	wantPath := encoding.SidecarIndexPath(cohort, []string{"id"})
	if env.Data.IndexPath != wantPath {
		t.Errorf("data.index_path = %q, want %q", env.Data.IndexPath, wantPath)
	}
	if env.Data.Cohort != cohort {
		t.Errorf("data.cohort = %q, want %q", env.Data.Cohort, cohort)
	}
	if len(env.Data.Keys) != 1 || env.Data.Keys[0] != "id" {
		t.Errorf("data.keys = %v, want [id]", env.Data.Keys)
	}
	if env.Data.DistinctKeys != 3 {
		t.Errorf("data.distinct_keys = %d, want 3", env.Data.DistinctKeys)
	}
	if env.Data.IndexedRecords != 3 {
		t.Errorf("data.indexed_records = %d, want 3", env.Data.IndexedRecords)
	}
}

func TestIndexBuildCLI_ShardArchiveErrorEnvelope(t *testing.T) {
	dir := t.TempDir()
	shard1 := filepath.Join(dir, "shard1.pulse")
	shard2 := filepath.Join(dir, "shard2.pulse")
	writeIndexTestCohort(t, shard1)
	writeIndexTestCohort(t, shard2)

	archive := filepath.Join(dir, "archive.pulse")
	p, err := pulse.New(pulse.Options{FS: afero.NewOsFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if err := p.CreateShardArchive(context.Background(), archive, []string{shard1, shard2}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	var buf bytes.Buffer
	err = runIndexCLI(t, &buf, "build", "--input", archive, "--key", "id", "--json")
	if err != nil {
		t.Fatalf("run (json mode should not return a Go error): %v", err)
	}

	var env struct {
		Errors []struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("json.Unmarshal: %v\nraw: %s", err, buf.String())
	}
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly 1 entry", env.Errors)
	}
	if env.Errors[0].Code != "PULSE_INDEX_UNSUPPORTED_SHARDED" {
		t.Errorf("errors[0].code = %q, want PULSE_INDEX_UNSUPPORTED_SHARDED", env.Errors[0].Code)
	}
}

func TestIndexBuildCLI_MissingKeyFlagErrors(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "cohort.pulse")
	writeIndexTestCohort(t, cohort)

	var buf bytes.Buffer
	err := runIndexCLI(t, &buf, "build", "--input", cohort)
	if err == nil {
		t.Fatal("expected error for missing required --key flag")
	}
}
