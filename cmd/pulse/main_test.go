package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// runApp runs the CLI with the given arguments and returns stdout and error.
func runApp(t *testing.T, args ...string) (string, error) {
	t.Helper()
	app := buildApp()
	var buf bytes.Buffer
	app.Writer = &buf
	// Propagate writer to all subcommands.
	setWriterRecursive(app, &buf)
	fullArgs := append([]string{"pulse"}, args...)
	err := app.Run(context.Background(), fullArgs)
	return buf.String(), err
}

// setWriterRecursive sets the Writer on all commands in the tree.
func setWriterRecursive(cmd *cli.Command, w *bytes.Buffer) {
	cmd.Writer = w
	for _, sub := range cmd.Commands {
		setWriterRecursive(sub, w)
	}
}

// createTestCSV writes a CSV file to the given path.
func createTestCSV(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data := "age,name\n10,alice\n20,bob\n30,charlie\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("writing test CSV: %v", err)
	}
	return path
}

// createTestPulseFile creates a .pulse file from CSV data at the given path.
func createTestPulseFile(t *testing.T, dir string) string {
	t.Helper()

	// Write CSV source
	csvPath := createTestCSV(t, dir, "source.csv")

	// Import to .pulse
	pulsePath := filepath.Join(dir, "test.pulse")
	fs := afero.NewOsFs()
	reader := csv.NewReader(fs, csvPath)
	job := pio.NewImportJob(reader, pulsePath)
	job.FS = fs

	_, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("creating test pulse file: %v", err)
	}
	return pulsePath
}

func TestCliHelp(t *testing.T) {
	out, err := runApp(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(out, "pulse") {
		t.Errorf("help output missing 'pulse': %s", out)
	}
}

func TestCliVersion(t *testing.T) {
	out, err := runApp(t, "--version")
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.Contains(out, "dev") {
		t.Errorf("version output missing 'dev': %s", out)
	}
}

func TestCliRootJson(t *testing.T) {
	out, err := runApp(t, "--json")
	if err != nil {
		t.Fatalf("--json: %v", err)
	}

	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}
}

func TestCliImportCsv(t *testing.T) {
	dir := t.TempDir()
	csvPath := createTestCSV(t, dir, "input.csv")
	pulsePath := filepath.Join(dir, "output.pulse")

	out, err := runApp(t, "import", "csv", "--input", csvPath, "--output", pulsePath)
	if err != nil {
		t.Fatalf("import csv: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Imported") {
		t.Errorf("expected 'Imported' in output: %s", out)
	}

	// Verify file exists
	if _, err := os.Stat(pulsePath); os.IsNotExist(err) {
		t.Error("output .pulse file not created")
	}
}

func TestCliImportPredict(t *testing.T) {
	dir := t.TempDir()
	csvPath := createTestCSV(t, dir, "input.csv")

	out, err := runApp(t, "import", "predict", "--input", csvPath, "--format", "csv", "--json")
	if err != nil {
		t.Fatalf("import predict: %v\noutput: %s", err, out)
	}

	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
}

func TestCliImportSchemaTemplate(t *testing.T) {
	dir := t.TempDir()
	csvPath := createTestCSV(t, dir, "input.csv")

	out, err := runApp(t, "import", "schema-template", csvPath)
	if err != nil {
		t.Fatalf("schema-template: %v\noutput: %s", err, out)
	}

	var fields []struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(out), &fields); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if len(fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(fields))
	}
	// Descriptions should be empty (template).
	for _, f := range fields {
		if f.Description != "" {
			t.Errorf("expected empty description for %s, got %q", f.Name, f.Description)
		}
	}
}

func TestCliExportCsv(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)
	outPath := filepath.Join(dir, "output.csv")

	out, err := runApp(t, "export", "csv", "--input", pulsePath, "--output", outPath)
	if err != nil {
		t.Fatalf("export csv: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Exported") {
		t.Errorf("expected 'Exported' in output: %s", out)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if len(data) == 0 {
		t.Error("output CSV is empty")
	}
}

func TestCliExportPredict(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	out, err := runApp(t, "export", "predict", "--input", pulsePath, "--json")
	if err != nil {
		t.Fatalf("export predict: %v\noutput: %s", err, out)
	}

	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
}

func TestCliConvert(t *testing.T) {
	dir := t.TempDir()
	csvPath := createTestCSV(t, dir, "input.csv")
	tsvPath := filepath.Join(dir, "output.tsv")

	out, err := runApp(t, "convert", csvPath, tsvPath)
	if err != nil {
		t.Fatalf("convert: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Converted") {
		t.Errorf("expected 'Converted' in output: %s", out)
	}

	data, err := os.ReadFile(tsvPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if len(data) == 0 {
		t.Error("output TSV is empty")
	}
}

func TestCliConvertPredict(t *testing.T) {
	dir := t.TempDir()
	csvPath := createTestCSV(t, dir, "input.csv")
	tsvPath := filepath.Join(dir, "output.tsv")

	out, err := runApp(t, "convert", "predict", csvPath, tsvPath)
	if err != nil {
		t.Fatalf("convert predict: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Schema:") {
		t.Errorf("expected 'Schema:' in output: %s", out)
	}
}

func TestCliCohortInspect(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	out, err := runApp(t, "cohort", "inspect", pulsePath)
	if err != nil {
		t.Fatalf("cohort inspect: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Fields:") {
		t.Errorf("expected 'Fields:' in output: %s", out)
	}
}

func TestCliCohortInspectJson(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	out, err := runApp(t, "cohort", "inspect", "--json", pulsePath)
	if err != nil {
		t.Fatalf("cohort inspect --json: %v\noutput: %s", err, out)
	}

	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}
}

func TestCliCohortInspectFullDict(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	out, err := runApp(t, "cohort", "inspect", "--full-dict", pulsePath)
	if err != nil {
		t.Fatalf("cohort inspect --full-dict: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Fields:") {
		t.Errorf("expected 'Fields:' in output: %s", out)
	}
}

func TestCliApiSample(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	out, err := runApp(t, "api", "sample", "--input", pulsePath, "--count", "2")
	if err != nil {
		t.Fatalf("api sample: %v\noutput: %s", err, out)
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if len(rows) > 2 {
		t.Errorf("expected at most 2 rows, got %d", len(rows))
	}
}

func TestCliApiFacet(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	out, err := runApp(t, "api", "facet", "--input", pulsePath, "--field", "name")
	if err != nil {
		t.Fatalf("api facet: %v\noutput: %s", err, out)
	}
	if out == "" {
		t.Error("expected facet output")
	}
}

func TestCliApiProcess(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	// Write request JSON
	reqJSON := `{
		"cohort": {"filename": "` + pulsePath + `"},
		"aggregations": [{"type": "AGG_COUNT", "field": "age"}]
	}`
	reqPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(reqPath, []byte(reqJSON), 0644); err != nil {
		t.Fatalf("writing request: %v", err)
	}

	out, err := runApp(t, "api", "process", "--request", reqPath)
	if err != nil {
		t.Fatalf("api process: %v\noutput: %s", err, out)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
}

func TestCliApiProcessJson(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	reqJSON := `{
		"cohort": {"filename": "` + pulsePath + `"},
		"aggregations": [{"type": "AGG_COUNT", "field": "age"}]
	}`
	reqPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(reqPath, []byte(reqJSON), 0644); err != nil {
		t.Fatalf("writing request: %v", err)
	}

	out, err := runApp(t, "api", "process", "--request", reqPath, "--json")
	if err != nil {
		t.Fatalf("api process --json: %v\noutput: %s", err, out)
	}

	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON envelope: %v\noutput: %s", err, out)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}
}

func TestCliApiCompose(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	reqJSON := `{
		"requests": [{
			"cohort": {"filename": "` + pulsePath + `"},
			"aggregations": [{"type": "AGG_COUNT", "field": "age"}]
		}]
	}`
	reqPath := filepath.Join(dir, "composed.json")
	if err := os.WriteFile(reqPath, []byte(reqJSON), 0644); err != nil {
		t.Fatalf("writing request: %v", err)
	}

	out, err := runApp(t, "api", "compose", "--request", reqPath)
	if err != nil {
		t.Fatalf("api compose: %v\noutput: %s", err, out)
	}

	// Default (non-JSON) output emits the ComposedResponse shape
	// {responses: [...], overlays: ...} — array-shaped decode would
	// fail since the top level is now an object.
	var composed struct {
		Responses []json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal([]byte(out), &composed); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if len(composed.Responses) == 0 {
		t.Errorf("expected non-empty responses, got: %s", out)
	}
}

func TestCliApiProcessStream(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	reqJSON := `{
		"cohort": {"filename": "` + pulsePath + `"},
		"aggregations": [{"type": "AGG_COUNT", "field": "age", "label": "n"}]
	}`
	reqPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(reqPath, []byte(reqJSON), 0644); err != nil {
		t.Fatalf("writing request: %v", err)
	}

	out, err := runApp(t, "api", "process", "--request", reqPath, "--stream")
	if err != nil {
		t.Fatalf("api process --stream: %v\noutput: %s", err, out)
	}

	// Each line is a single JSON row. AGG_COUNT yields exactly one row.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 NDJSON line, got %d (%q)", len(lines), out)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("invalid NDJSON line: %v\nline: %s", err, lines[0])
	}
	if _, ok := row["n"]; !ok {
		t.Errorf("missing label n in streamed row: %v", row)
	}
}

func TestCliApiComposeParallel(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	reqJSON := `{
		"requests": [
			{"cohort": {"filename": "` + pulsePath + `"}, "aggregations": [{"type": "AGG_COUNT", "field": "age", "label": "a"}]},
			{"cohort": {"filename": "` + pulsePath + `"}, "aggregations": [{"type": "AGG_COUNT", "field": "age", "label": "b"}]}
		]
	}`
	reqPath := filepath.Join(dir, "composed.json")
	if err := os.WriteFile(reqPath, []byte(reqJSON), 0644); err != nil {
		t.Fatalf("writing request: %v", err)
	}

	out, err := runApp(t, "api", "compose", "--request", reqPath, "--parallel", "2")
	if err != nil {
		t.Fatalf("api compose --parallel: %v\noutput: %s", err, out)
	}

	// Default (non-JSON) output emits the ComposedResponse shape
	// {responses: [...], overlays: ...}.
	var composed struct {
		Responses []map[string]any `json:"responses"`
	}
	if err := json.Unmarshal([]byte(out), &composed); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if len(composed.Responses) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(composed.Responses))
	}
}

func TestCliApiComposeStream(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	reqJSON := `{
		"requests": [
			{"cohort": {"filename": "` + pulsePath + `"}, "aggregations": [{"type": "AGG_COUNT", "field": "age", "label": "a"}]},
			{"cohort": {"filename": "` + pulsePath + `"}, "aggregations": [{"type": "AGG_COUNT", "field": "age", "label": "b"}]}
		]
	}`
	reqPath := filepath.Join(dir, "composed.json")
	if err := os.WriteFile(reqPath, []byte(reqJSON), 0644); err != nil {
		t.Fatalf("writing request: %v", err)
	}

	out, err := runApp(t, "api", "compose", "--request", reqPath, "--stream")
	if err != nil {
		t.Fatalf("api compose --stream: %v\noutput: %s", err, out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d (%q)", len(lines), out)
	}
	for i, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d invalid JSON: %v\n%s", i, err, line)
		}
		if entry["index"] == nil || entry["row"] == nil {
			t.Errorf("line %d missing index/row: %v", i, entry)
		}
	}
}

func TestCliApiPredict(t *testing.T) {
	dir := t.TempDir()
	pulsePath := createTestPulseFile(t, dir)

	reqJSON := `{
		"cohort": {"filename": "` + pulsePath + `"},
		"aggregations": [{"type": "AGG_COUNT", "field": "age"}]
	}`
	reqPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(reqPath, []byte(reqJSON), 0644); err != nil {
		t.Fatalf("writing request: %v", err)
	}

	out, err := runApp(t, "api", "predict", "--request", reqPath)
	if err != nil {
		t.Fatalf("api predict: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Valid:") {
		t.Errorf("expected 'Valid:' in output: %s", out)
	}
}

func TestCliSkillsList(t *testing.T) {
	out, err := runApp(t, "skills", "list")
	if err != nil {
		t.Fatalf("skills list: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "session-bootstrap") {
		t.Errorf("expected 'session-bootstrap' in output: %s", out)
	}
}

func TestCliSkillsListJson(t *testing.T) {
	out, err := runApp(t, "skills", "list", "--json")
	if err != nil {
		t.Fatalf("skills list --json: %v\noutput: %s", err, out)
	}

	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
}

func TestCliSkillsShow(t *testing.T) {
	out, err := runApp(t, "skills", "show", "session-bootstrap")
	if err != nil {
		t.Fatalf("skills show: %v\noutput: %s", err, out)
	}
	if out == "" {
		t.Error("expected skill content")
	}
}

func TestCliConvertCommand_E2E(t *testing.T) {
	// Full convert E2E: CSV -> TSV with --json output
	dir := t.TempDir()
	csvPath := createTestCSV(t, dir, "data.csv")
	tsvPath := filepath.Join(dir, "data.tsv")

	out, err := runApp(t, "convert", "--json", csvPath, tsvPath)
	if err != nil {
		t.Fatalf("convert --json: %v\noutput: %s", err, out)
	}

	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}

	// Verify TSV was created
	data, err := os.ReadFile(tsvPath)
	if err != nil {
		t.Fatalf("reading TSV: %v", err)
	}
	if !strings.Contains(string(data), "\t") {
		t.Error("output doesn't look like TSV")
	}
}

func TestCliApiProcessChain_Warnings(t *testing.T) {
	dir := t.TempDir()
	pulsePath := filepath.Join(dir, "chain_warn.pulse")

	// Build the .pulse file directly via the encoding package to lock
	// the schema shape — region=u8, score=f64. Region=2 carries two
	// zero-score rows so stage 1's FILTER_RANGE drops it; stage 2 then
	// has no region=2 row, surfacing the missing-reference trigger when
	// the overlay compares stage 0 (which still has region=2) against
	// stage 2 (which does not).
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{1, math.Float64bits(2.0)},
		{1, math.Float64bits(4.0)},
		{2, math.Float64bits(0.0)},
		{2, math.Float64bits(0.0)},
		{3, math.Float64bits(10.0)},
		{3, math.Float64bits(20.0)},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for ri, rec := range records {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				t.Fatalf("WriteFieldValue record[%d] field[%d]: %v", ri, fi, err)
			}
		}
	}
	if err := os.WriteFile(pulsePath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("writing .pulse: %v", err)
	}

	// Chain request: 3 stages, one whole-chain OVERLAY_INDEX_VS_STAGE
	// spec comparing stage 0 against stage 2 — stage 2 is missing
	// region=2 (filtered out at stage 1) so the SERIES handler emits a
	// deterministic ref_missing warning.
	chainJSON := `{
		"cohort": {"filename": "` + pulsePath + `"},
		"stages": [
			{
				"name": "stage_a",
				"request": {
					"groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
					"aggregations": [{"type": "AGG_SUM", "field": "score", "label": "v"}]
				}
			},
			{
				"name": "stage_b",
				"request": {
					"filterers": [{"type": "FILTER_RANGE", "field": "v", "values": ["0.5", "1000"]}],
					"groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
					"aggregations": [{"type": "AGG_SUM", "field": "v", "label": "w"}]
				}
			},
			{
				"name": "stage_c",
				"request": {
					"groups": [{"type": "GROUP_CATEGORY", "field": "region"}],
					"aggregations": [{"type": "AGG_COUNT", "field": "w", "label": "c"}]
				}
			}
		],
		"overlays": [
			{
				"name": "stage0_vs_stage2_index_warn",
				"kind": "OVERLAY_INDEX_VS_STAGE",
				"scope": "group",
				"ref": {"index": 2},
				"target": {"index": 0}
			}
		]
	}`
	reqPath := filepath.Join(dir, "chain.json")
	if err := os.WriteFile(reqPath, []byte(chainJSON), 0644); err != nil {
		t.Fatalf("writing chain request: %v", err)
	}

	out, err := runApp(t, "api", "process-chain", "--request", reqPath, "--json")
	if err != nil {
		t.Fatalf("api process-chain --json: %v\noutput: %s", err, out)
	}

	// Decode the envelope first so format_version + structural shape
	// stay intact — same pattern as TestCliApiProcessJson.
	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON envelope: %v\noutput: %s", err, out)
	}
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want 1.1", env.FormatVersion)
	}

	// data.overlays[0].warnings is the wire shape we're locking. The
	// envelope's Data is a typed *ChainResponse but JSON unmarshals it
	// into the generic Envelope as a map[string]any — re-decode through
	// the raw bytes to walk the keys.
	var raw struct {
		Data struct {
			Overlays []struct {
				Name     string                 `json:"name"`
				Kind     string                 `json:"kind"`
				Warnings []types.OverlayWarning `json:"warnings"`
			} `json:"overlays"`
			Stages []struct {
				Overlays []json.RawMessage `json:"overlays"`
			} `json:"stages"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("invalid data shape: %v\noutput: %s", err, out)
	}
	if got := len(raw.Data.Overlays); got != 1 {
		t.Fatalf("data.overlays length = %d, want 1 (one whole-chain spec → one layer)", got)
	}
	layer := raw.Data.Overlays[0]
	if got, want := layer.Kind, "OVERLAY_INDEX_VS_STAGE"; got != want {
		t.Errorf("data.overlays[0].kind = %q, want %q", got, want)
	}
	if got, want := layer.Name, "stage0_vs_stage2_index_warn"; got != want {
		t.Errorf("data.overlays[0].name = %q, want %q", got, want)
	}
	if len(layer.Warnings) == 0 {
		t.Fatalf("data.overlays[0].warnings = %v, want at least one PULSE_OVERLAY_REF_ZERO entry; envelope: %s",
			layer.Warnings, out)
	}
	sawRefZero := false
	for _, w := range layer.Warnings {
		if w.Code == "PULSE_OVERLAY_REF_ZERO" {
			sawRefZero = true
		}
		if w.Details == nil {
			t.Errorf("warning Details missing: %+v", w)
			continue
		}
		if _, ok := w.Details["overlay_index"]; !ok {
			t.Errorf("warning Details.overlay_index missing — dispatcher stamp dropped: %+v", w)
		}
	}
	if !sawRefZero {
		t.Errorf("no PULSE_OVERLAY_REF_ZERO entry in data.overlays[0].warnings; got: %+v", layer.Warnings)
	}

	// omitempty byte-identity guard: per-stage stage entries DO NOT
	// carry a per-stage Overlays slot (we only attached whole-chain
	// overlays), so the marshalled stages MUST NOT include an
	// "overlays" key. A raw substring scan over the JSON body of every
	// stage object catches an accidental empty-slice marshal that
	// would still flip the byte-identity gate at types/types_test.go.
	stagesIdx := strings.Index(out, "\"stages\"")
	if stagesIdx < 0 {
		t.Fatalf("envelope missing data.stages: %s", out)
	}
	stagesSlice := out[stagesIdx:]
	// The first "overlays" key after "stages" belongs to the
	// whole-chain ChainResponse.Overlays slot AT the same level —
	// scope the per-stage scan to JSON objects nested under stages
	// (heuristic: any "overlays" appearing before the closing of the
	// stages array). Walk byte-by-byte tracking bracket depth.
	depth := 0
	stageOverlaysFound := false
	inString := false
	prevEsc := false
	// Track when we're inside the stages array (depth 1 relative to
	// the start of "stages") so we only flag per-stage overlay keys.
	for i := 0; i < len(stagesSlice); i++ {
		c := stagesSlice[i]
		if inString {
			if prevEsc {
				prevEsc = false
				continue
			}
			if c == '\\' {
				prevEsc = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
			// Look for "overlays" key inside the stages array (depth>=2).
			if depth >= 2 && i+10 <= len(stagesSlice) && stagesSlice[i:i+10] == "\"overlays\"" {
				stageOverlaysFound = true
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				// Exited the stages array.
				goto doneScan
			}
		}
	}
doneScan:
	if stageOverlaysFound {
		t.Errorf("per-stage overlays key leaked into envelope when no per-stage overlays were requested — omitempty contract violated:\n%s", out)
	}
}

func TestCliApiCompose_PairwiseZMatrix_OverlayReachesEnvelope(t *testing.T) {
	dir := t.TempDir()
	pulsePath := importExperimentCohort(t, dir)
	exampleSrc := readRepoFile(t, "examples/overlays/pairwise-z-matrix.json")
	composedPath := rewriteComposedExample(t, dir, exampleSrc, pulsePath, "composed_z.json")

	out, err := runApp(t, "api", "compose", "--request", composedPath, "--json")
	if err != nil {
		t.Fatalf("api compose --json: %v\noutput: %s", err, out)
	}

	env, layer, matrix := readPairwiseEnvelope(t, out, "OVERLAY_Z_CELL")
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want \"1.1\"", env.FormatVersion)
	}
	if got, want := layer.Kind, "OVERLAY_Z_CELL"; got != want {
		t.Errorf("data.overlays[0].kind = %q, want %q", got, want)
	}

	assertPairwiseByteEqual(t, pulsePath, matrix, "TEST_Z_TWO_SAMPLE", "z")
}

func TestCliApiCompose_PairwiseWelchMatrix_OverlayReachesEnvelope(t *testing.T) {
	dir := t.TempDir()
	pulsePath := importExperimentCohort(t, dir)
	exampleSrc := readRepoFile(t, "examples/overlays/pairwise-welch-matrix.json")
	composedPath := rewriteComposedExample(t, dir, exampleSrc, pulsePath, "composed_welch.json")

	out, err := runApp(t, "api", "compose", "--request", composedPath, "--json")
	if err != nil {
		t.Fatalf("api compose --json: %v\noutput: %s", err, out)
	}

	env, layer, matrix := readPairwiseEnvelope(t, out, "OVERLAY_T_CELL")
	if env.FormatVersion != "1.1" {
		t.Errorf("format_version = %q, want \"1.1\"", env.FormatVersion)
	}
	if got, want := layer.Kind, "OVERLAY_T_CELL"; got != want {
		t.Errorf("data.overlays[0].kind = %q, want %q", got, want)
	}

	assertPairwiseByteEqual(t, pulsePath, matrix, "TEST_WELCH", "welch")
}

// repoRoot returns the absolute path to the repository root from the
// package test working directory (cmd/pulse/...). Used by the pairwise
// gates to locate the canonical example fixtures without hard-coding
// the os-specific path layout. Failing here is unrecoverable — the
// test relies on examples/ + examples/fixtures/ being present, which
// is true on every dev / CI checkout.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	// cmd/pulse → ../../ to repo root.
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// readRepoFile reads a file from the repo root and returns its bytes.
func readRepoFile(t *testing.T, relPath string) []byte {
	t.Helper()
	path := filepath.Join(repoRoot(t), relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", relPath, err)
	}
	return data
}

// importExperimentCohort builds the experiment.pulse cohort the
// pairwise example requests reference. Re-uses the checked-in
// examples/fixtures/experiment.csv + examples/fixtures/schemas/
// experiment.json so the schema (treatment / region / segment as
// categorical_u8; revenue as f64) matches the example request slot
// fields exactly.
//
// Returns the absolute path to the produced .pulse file. The caller
// hands this path to rewriteComposedExample so the example points at
// the hermetic cohort instead of the repo-level .data/ directory the
// examples expect at runtime.
func importExperimentCohort(t *testing.T, dir string) string {
	t.Helper()
	root := repoRoot(t)
	csvPath := filepath.Join(root, "examples", "fixtures", "experiment.csv")
	schemaPath := filepath.Join(root, "examples", "fixtures", "schemas", "experiment.json")
	pulsePath := filepath.Join(dir, "experiment.pulse")

	out, err := runApp(t, "import", "csv",
		"--input", csvPath,
		"--output", pulsePath,
		"--schema", schemaPath,
		"--json")
	if err != nil {
		t.Fatalf("import csv (experiment): %v\noutput: %s", err, out)
	}
	if _, err := os.Stat(pulsePath); err != nil {
		t.Fatalf("experiment.pulse not produced: %v", err)
	}
	return pulsePath
}

// rewriteComposedExample loads the original example body and rewrites
// every request slot's cohort to point at the temp pulse file the test
// fixture produced. The original example references
// {filename: "experiment.pulse", data_dir: ".data"} which only works
// after examples/fixtures/build.sh has populated the repo-level .data
// directory; hermetic tests need an absolute path on a temp file
// instead. Returns the absolute path to the rewritten request JSON.
func rewriteComposedExample(t *testing.T, dir string, body []byte, pulsePath, name string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("unmarshal example: %v", err)
	}
	reqs, ok := doc["requests"].([]any)
	if !ok || len(reqs) == 0 {
		t.Fatalf("example missing requests slice: %v", doc)
	}
	for i, r := range reqs {
		rm, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("requests[%d] not an object: %T", i, r)
		}
		rm["cohort"] = map[string]any{"filename": pulsePath}
	}
	// Strip _meta so the request loader does not have to handle it.
	delete(doc, "_meta")

	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal rewritten: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatalf("write rewritten: %v", err)
	}
	return path
}

// readPairwiseEnvelope decodes the api-compose JSON envelope and
// extracts the first overlay layer plus its matrix payload. Fails the
// test if any of the structural assumptions (envelope shape,
// non-empty overlays, matrix-shape payload) are not met — the gate's
// whole point is that the discard is gone and these structures are
// reachable.
func readPairwiseEnvelope(t *testing.T, out, wantKind string) (descriptor.Envelope, pairwiseLayer, *types.MatrixPayload) {
	t.Helper()
	var env descriptor.Envelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON envelope: %v\noutput: %s", err, out)
	}
	var raw struct {
		Data struct {
			Responses []json.RawMessage `json:"responses"`
			Overlays  []pairwiseLayer   `json:"overlays"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("invalid data shape: %v\noutput: %s", err, out)
	}
	if got := len(raw.Data.Responses); got != 2 {
		t.Fatalf("data.responses length = %d, want 2 (control + variant): %s", got, out)
	}
	if got := len(raw.Data.Overlays); got != 1 {
		t.Fatalf("data.overlays length = %d, want 1; the discard would surface as 0 here: %s", got, out)
	}
	layer := raw.Data.Overlays[0]
	if layer.Kind != wantKind {
		t.Errorf("overlay kind = %q, want %q", layer.Kind, wantKind)
	}
	if layer.Payload.Shape != "matrix" {
		t.Fatalf("overlay payload shape = %q, want \"matrix\"", layer.Payload.Shape)
	}
	if layer.Payload.Matrix == nil {
		t.Fatalf("overlay payload matrix is nil — handler emitted empty payload")
	}
	return env, layer, layer.Payload.Matrix
}

// pairwiseLayer mirrors the OverlayLayer shape we need to inspect in
// the CLI envelope. Reuses types.MatrixPayload + types.OverlayWarning
// directly so the structural contract stays locked to the production
// surface.
type pairwiseLayer struct {
	Name    string                 `json:"name"`
	Kind    string                 `json:"kind"`
	Scope   string                 `json:"scope"`
	Payload pairwiseOverlayPayload `json:"payload"`
}

type pairwiseOverlayPayload struct {
	Shape  string               `json:"shape"`
	Matrix *types.MatrixPayload `json:"matrix,omitempty"`
}

// assertPairwiseByteEqual iterates every present cell in the overlay
// matrix and asserts the p-value byte-equals the canonical row-test
// surface for the same (region, segment) cell. The row test is invoked
// via a second `pulse api process --request <file> --json` call per
// cell (FILTER_INCLUDE region=<row_key> + FILTER_INCLUDE segment=
// <col_key> + a single split-by-treatment test) — same plumbing
// downstream callers exercise.
//
// kindName labels the row-test type used (TEST_Z_TWO_SAMPLE or
// TEST_WELCH); shortName labels the test in error messages.
func assertPairwiseByteEqual(t *testing.T, pulsePath string, matrix *types.MatrixPayload, kindName, shortName string) {
	t.Helper()
	if len(matrix.RowKeys) == 0 || len(matrix.ColumnKeys) == 0 {
		t.Fatalf("matrix has no row/column keys; overlay produced nothing to verify")
	}
	if len(matrix.Cells) != len(matrix.RowKeys) {
		t.Fatalf("matrix cells row count %d != row keys %d", len(matrix.Cells), len(matrix.RowKeys))
	}

	verified := 0
	for r, rowKey := range matrix.RowKeys {
		region := axisKeyToScalarString(t, rowKey)
		for c, colKey := range matrix.ColumnKeys {
			segment := axisKeyToScalarString(t, colKey)
			if c >= len(matrix.Cells[r]) {
				continue
			}
			cell := matrix.Cells[r][c]
			if !cell.Present {
				continue
			}
			overlayP, ok := cell.Value.(float64)
			if !ok {
				t.Fatalf("cell (%d,%d) value not float64: %T (%v)", r, c, cell.Value, cell.Value)
			}
			if math.IsNaN(overlayP) {
				// Degenerate cell — both surfaces decline; nothing to
				// byte-compare here. The row test would have raised
				// PULSE_TEST_INSUFFICIENT_N / PULSE_TEST_VARIANCE_ZERO
				// for the same inputs.
				continue
			}
			rowP := runRowTestForCell(t, pulsePath, region, segment, kindName)
			bitsOverlay := math.Float64bits(overlayP)
			bitsRow := math.Float64bits(rowP)
			if bitsOverlay != bitsRow {
				t.Errorf("byte-equal parity violated at (region=%s, segment=%s): overlay p=%v (bits=0x%x), %s p=%v (bits=0x%x)",
					region, segment, overlayP, bitsOverlay, shortName, rowP, bitsRow)
			}
			verified++
		}
	}
	if verified == 0 {
		t.Fatalf("no overlay cells could be compared — every present cell was NaN; the gate locked nothing")
	}
}

// axisKeyToScalarString collapses a single-grouper AxisKey (which is
// always length 1 in the pairwise examples — one GROUP_CATEGORY per
// axis) to its underlying string label. Fails the test if the shape
// drifts (multi-grouper axes, non-string keys).
func axisKeyToScalarString(t *testing.T, key types.AxisKey) string {
	t.Helper()
	if len(key) != 1 {
		t.Fatalf("expected single-grouper axis key, got %d entries: %v", len(key), key)
	}
	switch v := key[0].(type) {
	case string:
		return v
	default:
		t.Fatalf("axis key entry not a string: %T (%v)", v, v)
		return ""
	}
}

// runRowTestForCell invokes `pulse api process --json` against the
// same cohort with FILTER_INCLUDE region=<region> +
// FILTER_INCLUDE segment=<segment> and a single split-by-treatment
// row test of the named kind. Returns the p-value the row test
// emits — the canonical surface the overlay handler is byte-equal to
// by construction.
func runRowTestForCell(t *testing.T, pulsePath, region, segment, testKind string) float64 {
	t.Helper()
	dir := filepath.Dir(pulsePath)
	reqBody := map[string]any{
		"cohort": map[string]any{"filename": pulsePath},
		"filterers": []any{
			map[string]any{"type": "FILTER_INCLUDE", "field": "region", "values": []string{region}},
			map[string]any{"type": "FILTER_INCLUDE", "field": "segment", "values": []string{segment}},
		},
		"tests": []any{
			map[string]any{
				"type":     testKind,
				"field":    "revenue",
				"split_by": "treatment",
				"alpha":    0.05,
			},
		},
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal row-test request: %v", err)
	}
	// Unique filename per cell so concurrent fixtures cannot collide
	// (the table is small but t.Parallel may land later).
	reqPath := filepath.Join(dir, "rowtest_"+region+"_"+segment+"_"+testKind+".json")
	if err := os.WriteFile(reqPath, raw, 0644); err != nil {
		t.Fatalf("write row-test request: %v", err)
	}
	out, err := runApp(t, "api", "process", "--request", reqPath, "--json")
	if err != nil {
		t.Fatalf("api process (%s region=%s segment=%s): %v\noutput: %s", testKind, region, segment, err, out)
	}
	var raw2 struct {
		Data struct {
			Tests []struct {
				Type   string  `json:"type"`
				PValue float64 `json:"p_value"`
			} `json:"tests"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &raw2); err != nil {
		t.Fatalf("row-test envelope decode: %v\noutput: %s", err, out)
	}
	if len(raw2.Data.Tests) == 0 {
		t.Fatalf("row-test produced no tests for region=%s segment=%s: %s", region, segment, out)
	}
	if raw2.Data.Tests[0].Type != testKind {
		t.Fatalf("row-test type = %q, want %q", raw2.Data.Tests[0].Type, testKind)
	}
	return raw2.Data.Tests[0].PValue
}

// Ensure unused imports are consumed.
var _ = encoding.FieldTypeU8
var _ = (*afero.MemMapFs)(nil)
