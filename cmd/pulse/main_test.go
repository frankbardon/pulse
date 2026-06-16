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
	// session-bootstrap is the post-E4 anchor (legacy: getting-started).
	// Asserts the list output includes at least one canonical skill.
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

// TestCliApiProcessChain_Warnings is the E2-S2 CLI integration gate.
// Drives `pulse api process-chain --json` against a chain request
// engineered to deterministically emit at least one chain-overlay
// warning, and asserts the warning lands on `data.overlays[i].warnings`
// in the JSON envelope.
//
// Deterministic trigger (same shape the service-layer parity test in
// service/chain_test.go uses — missing-reference row, NOT zero-divisor;
// the zero-divisor SERIES path injects NaN into the payload which
// blocks JSON marshalling at the CLI boundary):
//
//   - Hermetic 6-row .pulse file with region (u8) and score (f64)
//     columns. Region=2 has two zero-score rows so stage 0's AGG_SUM
//     emits 0 for region=2.
//   - 3-stage SERIES chain. Stage 1 carries a FILTER_RANGE v>0.5 that
//     drops the region=2 row before re-aggregating; stage 2's
//     AGG_COUNT only sees region=1 and region=3.
//   - One whole-chain OVERLAY_INDEX_VS_STAGE spec, Target=stage 0
//     (3 rows incl. region=2), Ref=stage 2 (2 rows). The region=2
//     target row has no matching reference → SERIES handler emits ONE
//     PULSE_OVERLAY_REF_ZERO warning with ref_missing=true and SKIPS
//     the entry (no NaN substitution per
//     processing/overlay_index_vs_stage.go:518–531).
//   - Same overlay kind + same input always emits the same warning
//     code; trigger is structural not timing-dependent.
//
// The envelope assertion has two layers:
//
//  1. data.overlays[0].warnings is present and non-empty — the wire
//     shape that downstream callers (JSON consumers, embedders) actually
//     observe must reflect what ProcessChain returned.
//  2. The warning carries the canonical PULSE_OVERLAY_REF_ZERO code so
//     test-coverage assertions against the catalog stay stable.
//
// E2-S1 (service layer) and E2-S2 (this test) together guarantee that
// every chain-overlay warning emitted by processing.ApplyChainOverlays
// reaches the CLI envelope without loss.
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

// Ensure unused imports are consumed.
var _ = encoding.FieldTypeU8
var _ = (*afero.MemMapFs)(nil)
