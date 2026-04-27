package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
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
	if env.FormatVersion != "1.0" {
		t.Errorf("format_version = %q, want 1.0", env.FormatVersion)
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
	if env.FormatVersion != "1.0" {
		t.Errorf("format_version = %q, want 1.0", env.FormatVersion)
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
	if env.FormatVersion != "1.0" {
		t.Errorf("format_version = %q, want 1.0", env.FormatVersion)
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

	// Should be array of responses.
	var responses []json.RawMessage
	if err := json.Unmarshal([]byte(out), &responses); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out)
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
	if !strings.Contains(out, "getting-started") {
		t.Errorf("expected 'getting-started' in output: %s", out)
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
	out, err := runApp(t, "skills", "show", "getting-started")
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
	if env.FormatVersion != "1.0" {
		t.Errorf("format_version = %q, want 1.0", env.FormatVersion)
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

// Ensure unused imports are consumed.
var _ = encoding.FieldTypeU8
var _ = (*afero.MemMapFs)(nil)
