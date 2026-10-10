package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
)

// seedByNameImport writes a CSV with an extra column and a schema that
// names only two of its columns, in the opposite order.
func seedByNameImport(t *testing.T) (csvPath, schemaPath, outPath string) {
	t.Helper()
	dir := t.TempDir()
	csvPath = filepath.Join(dir, "src.csv")
	if err := os.WriteFile(csvPath, []byte("a,b,z\n1,2,x\n3,4,y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	schemaPath = filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`[{"name":"b","type":"u8"},{"name":"a","type":"u8"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	return csvPath, schemaPath, filepath.Join(dir, "out.pulse")
}

func envelopeErrorCode(t *testing.T, buf *bytes.Buffer) string {
	t.Helper()
	var env struct {
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, buf.String())
	}
	if len(env.Errors) == 0 {
		return ""
	}
	return env.Errors[0].Code
}

// TestImportCLI_ByNameRefusalAndIgnoreExtra: every leaf that takes
// --schema carries the refusal's own code on the envelope, and
// --ignore-extra reaches the job.
func TestImportCLI_ByNameRefusalAndIgnoreExtra(t *testing.T) {
	csvPath, schemaPath, outPath := seedByNameImport(t)
	unmapped := string(perrors.PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED)

	leaves := []struct {
		name string
		run  func(buf *bytes.Buffer, extra ...string) error
	}{
		{"import csv", func(buf *bytes.Buffer, extra ...string) error {
			return runImportCSV(t, buf, append([]string{"csv", "-i", csvPath, "-o", outPath, "--schema", schemaPath, "--json"}, extra...)...)
		}},
		{"import predict", func(buf *bytes.Buffer, extra ...string) error {
			return runImportCSV(t, buf, append([]string{"predict", "-i", csvPath, "--schema", schemaPath, "--json"}, extra...)...)
		}},
		{"convert", func(buf *bytes.Buffer, extra ...string) error {
			root := ConvertCommand()
			root.Writer = buf
			args := append([]string{"convert", "--schema", schemaPath, "--json"}, extra...)
			return root.Run(context.Background(), append(args, csvPath, filepath.Join(filepath.Dir(outPath), "out.ndjson")))
		}},
	}
	for _, leaf := range leaves {
		t.Run(leaf.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := leaf.run(&buf); err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if got := envelopeErrorCode(t, &buf); got != unmapped {
				t.Fatalf("errors[0].code = %q, want %s\n%s", got, unmapped, buf.String())
			}
			buf.Reset()
			if err := leaf.run(&buf, "--ignore-extra"); err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if got := envelopeErrorCode(t, &buf); got != "" {
				t.Fatalf("--ignore-extra still refused: %s\n%s", got, buf.String())
			}
		})
	}
}
