package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// schemaLeaves runs each leaf that loads a --schema file / takes --float
// against csvPath, returning the JSON envelope in buf.
func schemaLeaves(t *testing.T, csvPath, outDir string) []struct {
	name string
	run  func(buf *bytes.Buffer, extra ...string) error
} {
	t.Helper()
	return []struct {
		name string
		run  func(buf *bytes.Buffer, extra ...string) error
	}{
		{"import csv", func(buf *bytes.Buffer, extra ...string) error {
			return runImportCSV(t, buf, append([]string{"csv", "-i", csvPath, "-o", filepath.Join(outDir, "out.pulse"), "--json"}, extra...)...)
		}},
		{"import predict", func(buf *bytes.Buffer, extra ...string) error {
			return runImportCSV(t, buf, append([]string{"predict", "-i", csvPath, "--json"}, extra...)...)
		}},
		{"convert", func(buf *bytes.Buffer, extra ...string) error {
			root := ConvertCommand()
			root.Writer = buf
			args := append([]string{"convert", "--json"}, extra...)
			return root.Run(context.Background(), append(args, csvPath, filepath.Join(outDir, "out.ndjson")))
		}},
	}
}

// TestImportCLI_UnknownSchemaTypeRefused: a schema file naming a type
// that is not registered is refused on every loader with its own code —
// the old loader silently stored the column as f64.
func TestImportCLI_UnknownSchemaTypeRefused(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "src.csv")
	if err := os.WriteFile(csvPath, []byte("a,b\n1,2.5\n3,4.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(`[{"name":"a","type":"u8"},{"name":"b","type":"float"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	want := string(perrors.PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN)
	for _, leaf := range schemaLeaves(t, csvPath, dir) {
		t.Run(leaf.name, func(t *testing.T) {
			var buf bytes.Buffer
			wantEnvelopeFatal(t, leaf.run(&buf, "--schema", schemaPath))
			if got := envelopeErrorCode(t, &buf); got != want {
				t.Fatalf("errors[0].code = %q, want %s\n%s", got, want, buf.String())
			}
		})
	}
}

// TestImportCLI_FloatFlag: --float reaches every inferring leaf — an
// unknown width is refused with its own code — and the default infers
// f64 while --float f32 opts the column into f32.
func TestImportCLI_FloatFlag(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "src.csv")
	if err := os.WriteFile(csvPath, []byte("price\n0.1\n123456.789012\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := string(perrors.PULSE_IMPORT_FLOAT_WIDTH_INVALID)
	for _, leaf := range schemaLeaves(t, csvPath, dir) {
		t.Run(leaf.name, func(t *testing.T) {
			var buf bytes.Buffer
			wantEnvelopeFatal(t, leaf.run(&buf, "--float", "f16"))
			if got := envelopeErrorCode(t, &buf); got != want {
				t.Fatalf("errors[0].code = %q, want %s\n%s", got, want, buf.String())
			}
		})
	}

	out := filepath.Join(dir, "out.pulse")
	for flag, wantType := range map[string]encoding.FieldType{"": encoding.FieldTypeF64, "f64": encoding.FieldTypeF64, "f32": encoding.FieldTypeF32} {
		args := []string{"csv", "-i", csvPath, "-o", out, "--json"}
		if flag != "" {
			args = append(args, "--float", flag)
		}
		var buf bytes.Buffer
		if err := runImportCSV(t, &buf, args...); err != nil {
			t.Fatalf("--float %q: %v", flag, err)
		}
		f, err := os.Open(out)
		if err != nil {
			t.Fatal(err)
		}
		schema, _, err := encx.ReadPreamble(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got := schema.Fields[0].Type; got != wantType {
			t.Errorf("--float %q: price = %s, want %s", flag, got, wantType)
		}
	}
}

// TestImportCLI_SchemaTemplateFloat: schema-template inherits the f64
// default and honours --float f32.
func TestImportCLI_SchemaTemplateFloat(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "src.csv")
	if err := os.WriteFile(csvPath, []byte("price\n0.1\n123456.789012\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for flag, want := range map[string]string{"": "f64", "f32": "f32"} {
		args := []string{"schema-template", csvPath}
		if flag != "" {
			args = []string{"schema-template", "--float", flag, csvPath}
		}
		var buf bytes.Buffer
		if err := runImportCSV(t, &buf, args...); err != nil {
			t.Fatalf("--float %q: %v", flag, err)
		}
		var tmpl []struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(buf.Bytes(), &tmpl); err != nil || len(tmpl) != 1 {
			t.Fatalf("template %q: %v", buf.String(), err)
		}
		if tmpl[0].Type != want {
			t.Errorf("--float %q: type %q, want %q", flag, tmpl[0].Type, want)
		}
	}
}
