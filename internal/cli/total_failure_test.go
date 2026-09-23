package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	pformat "github.com/frankbardon/pulse/io/format"
	"github.com/spf13/afero"
)

// seedTotalFailureImport writes a CSV whose every data row is unparseable
// under the accompanying explicit schema, and returns (csv, schema, out)
// paths.
func seedTotalFailureImport(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()

	csvPath := filepath.Join(dir, "bad.csv")
	if err := os.WriteFile(csvPath, []byte("n\nabc\ndef\nghi\n"), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`[{"name":"n","type":"u8"}]`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return csvPath, schemaPath, filepath.Join(dir, "out.pulse")
}

func runImportCSV(t *testing.T, out *bytes.Buffer, args ...string) error {
	t.Helper()
	root := ImportCommand()
	if out != nil {
		root.Writer = out
	}
	return root.Run(context.Background(), append([]string{"import"}, args...))
}

// TestImportCLI_TotalFailure_TextArmExitsNonZero is the exit-code half of
// the E3-S7 behaviour change. `pulse import csv` over a source where every
// row fails used to print "Imported 0 rows" and exit 0. It now returns the
// error, which cmd/pulse turns into a non-zero exit.
func TestImportCLI_TotalFailure_TextArmExitsNonZero(t *testing.T) {
	csvPath, schemaPath, outPath := seedTotalFailureImport(t)

	var buf bytes.Buffer
	err := runImportCSV(t, &buf, "csv", "-i", csvPath, "-o", outPath, "--schema", schemaPath)
	if err == nil {
		t.Fatalf("`pulse import csv` returned nil over an all-bad source; stdout = %q", buf.String())
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Error("out.pulse was written despite total import failure")
	}
}

// TestImportCLI_TotalFailure_EnvelopeCarriesRealCode is the `--json` half.
// writeCodedErrorEnvelope unwraps a *errors.CodedError with errors.As and
// falls back to the leaf placeholder only for an UNCODED error, so the
// total-failure verdict must arrive coded: an envelope carrying
// "IMPORT_ERROR" is unusable with `pulse errors lookup`.
func TestImportCLI_TotalFailure_EnvelopeCarriesRealCode(t *testing.T) {
	csvPath, schemaPath, outPath := seedTotalFailureImport(t)

	var buf bytes.Buffer
	if err := runImportCSV(t, &buf, "csv", "-i", csvPath, "-o", outPath, "--schema", schemaPath, "--json"); err != nil {
		t.Fatalf("json arm returned a transport error: %v", err)
	}

	var env struct {
		FormatVersion string `json:"format_version"`
		Errors        []struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, buf.String())
	}
	if len(env.Errors) != 1 {
		t.Fatalf("errors = %d, want 1: %s", len(env.Errors), buf.String())
	}
	if env.Errors[0].Code != string(perrors.PULSE_IMPORT_ROW_ERROR) {
		t.Errorf("errors[0].code = %q, want %q — a leaf placeholder cannot be looked up",
			env.Errors[0].Code, perrors.PULSE_IMPORT_ROW_ERROR)
	}
	if _, ok := perrors.ParseCode(env.Errors[0].Code); !ok {
		t.Errorf("errors[0].code = %q is not a registered code; `pulse errors lookup` would fail", env.Errors[0].Code)
	}
	if got := env.Errors[0].Details["rows_failed"]; got != float64(3) {
		t.Errorf("details[rows_failed] = %v, want 3", got)
	}
}

// TestExportTargets_EmitNothingBeforeClose is why the io leaves do NOT
// close the writer on an error return, and why E3-S7 left that alone.
//
// The concern raised was that `runExport` / the convert leaf return before
// writer.Close() on a Run error, leaving a partially-written target file
// unclosed — and that the total-failure verdict makes that path far more
// reachable. It turns out the premise does not hold here: every adapter
// newWriterForFormat can build buffers its output in memory and emits the
// file exactly once, inside Close (afero.WriteFile, not an incrementally
// written handle). Skipping Close therefore writes NOTHING and leaks no
// handle, while ADDING a close-on-error would write a zero-row target next
// to a hard error — precisely the trap the import arm avoids by refusing
// before its cohort write.
//
// This test pins the property the decision rests on. An adapter that
// starts streaming to disk before Close breaks it, and must then bring its
// own cleanup rather than silently leaving a truncated file behind.
func TestExportTargets_EmitNothingBeforeClose(t *testing.T) {
	formats := []string{
		pformat.CSV, pformat.TSV, pformat.NDJSON, pformat.JSONArray,
		pformat.Parquet, pformat.Arrow, pformat.Excel, pformat.SPSS,
	}
	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			const path = "out.target"
			w, err := newWriterForFormat(format, fs, path, writerOptions{})
			if err != nil {
				t.Fatalf("newWriterForFormat(%s): %v", format, err)
			}
			if err := w.WriteHeader([]string{"n"}); err != nil {
				t.Fatalf("WriteHeader: %v", err)
			}
			// No Close — this is the io leaves' error path.
			if ok, _ := afero.Exists(fs, path); ok {
				t.Errorf("%s wrote %s before Close; the leaves' error return would leave a truncated file behind", format, path)
			}
		})
	}
}
