package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// writeReaderCohort writes a one-row cohort for the reader-facing
// output tests (U44 #310): id (u8), a nullable f64, a decimal128(12,2),
// a date and a datetime. Row: id 1, price 9.5, amt 12.34,
// d 2026-01-01, ts 2026-01-02T03:04:05Z.
func writeReaderCohort(t *testing.T, dir string) string {
	t.Helper()
	sch := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, ByteOffset: 0},
		{Name: "price", Type: encoding.FieldTypeF64, Nullable: true, ByteOffset: 1},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 12, Scale: 2, ByteOffset: 9},
		{Name: "d", Type: encoding.FieldTypeDate, ByteOffset: 25},
		{Name: "ts", Type: encoding.FieldTypeDateTime, ByteOffset: 29},
	}}
	var buf bytes.Buffer
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(encx.WritePreamble(&buf, sch))
	must(encoding.WriteFieldValue(&buf, encoding.FieldTypeU8, 1))
	must(encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(9.5)))
	must(encoding.WriteDecimal128(&buf, encoding.NewDecimal128FromInt(1234)))
	must(encoding.WriteFieldValue(&buf, encoding.FieldTypeDate, 20454))
	must(encoding.WriteFieldValue(&buf, encoding.FieldTypeDateTime, 1767323045))
	must(encoding.WriteBitmap(&buf, make([]byte, sch.BitmapByteSize())))
	path := filepath.Join(dir, "reader.pulse")
	must(os.WriteFile(path, buf.Bytes(), 0o644))
	return path
}

// TestCliCohortInspect_ShowsNullabilityAndDecimalShape: both inspect
// modes say which fields are nullable, and text mode prints a
// decimal128 field's precision and scale (JSON already carried them).
func TestCliCohortInspect_ShowsNullabilityAndDecimalShape(t *testing.T) {
	t.Parallel()

	path := writeReaderCohort(t, t.TempDir())

	out, err := runApp(t, "cohort", "inspect", "--json", path)
	if err != nil {
		t.Fatalf("cohort inspect --json: %v\n%s", err, out)
	}
	var env struct {
		Data descriptor.InspectResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	nullable := map[string]bool{}
	for _, f := range env.Data.Fields {
		nullable[f.Name] = f.Nullable
	}
	if !nullable["price"] || nullable["id"] || nullable["amt"] {
		t.Errorf("json nullable = %v, want only price", nullable)
	}
	if !strings.Contains(out, `"nullable": false`) {
		t.Errorf("json must carry nullable on every field, false included:\n%s", out)
	}

	out, err = runApp(t, "cohort", "inspect", path)
	if err != nil {
		t.Fatalf("cohort inspect: %v\n%s", err, out)
	}
	lines := strings.Split(out, "\n")
	block := func(name string) string {
		for i, l := range lines {
			if fs := strings.Fields(l); len(fs) > 0 && fs[0] == name && strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "    ") {
				var b strings.Builder
				for _, sub := range lines[i+1:] {
					if !strings.HasPrefix(sub, "    ") {
						break
					}
					b.WriteString(sub + "\n")
				}
				return b.String()
			}
		}
		t.Fatalf("no field line for %s:\n%s", name, out)
		return ""
	}
	if got := block("price"); !strings.Contains(got, "nullable: yes") {
		t.Errorf("text price block %q, want nullable: yes\n%s", got, out)
	}
	if got := block("id"); strings.Contains(got, "nullable") {
		t.Errorf("text id block %q mentions nullable for a non-nullable field", got)
	}
	if got := block("amt"); !strings.Contains(got, "decimal: precision 12, scale 2") {
		t.Errorf("text amt block %q, want the decimal shape\n%s", got, out)
	}
}

// TestCliRawRows_RenderCalendarLiterals: `api sample` and `api lookup`
// render date as YYYY-MM-DD and datetime as RFC 3339 UTC, never the
// epoch day / second numbers.
func TestCliRawRows_RenderCalendarLiterals(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := t.TempDir()
	path := writeReaderCohort(t, dir)
	t.Setenv("PULSE_DATA_DIR", dir)

	check := func(surface string, row map[string]any) {
		t.Helper()
		if row["d"] != "2026-01-01" || row["ts"] != "2026-01-02T03:04:05Z" {
			t.Errorf("%s row d=%#v ts=%#v, want 2026-01-01 / 2026-01-02T03:04:05Z", surface, row["d"], row["ts"])
		}
	}

	out, err := runApp(t, "api", "sample", "--input", path, "--count", "1", "--json")
	if err != nil {
		t.Fatalf("api sample: %v\n%s", err, out)
	}
	var sample struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &sample); err != nil || len(sample.Data) != 1 {
		t.Fatalf("sample envelope: %v\n%s", err, out)
	}
	check("sample --json", sample.Data[0])

	out, err = runApp(t, "api", "sample", "--input", path, "--count", "1")
	if err != nil {
		t.Fatalf("api sample: %v\n%s", err, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("sample text: %v\n%s", err, out)
	}
	check("sample", rows[0])

	if out, err := runApp(t, "index", "build", "--input", path, "--key", "id"); err != nil {
		t.Fatalf("index build: %v\n%s", err, out)
	}
	out, err = runApp(t, "api", "lookup", "--input", path, "--key", "id=1", "--json")
	if err != nil {
		t.Fatalf("api lookup: %v\n%s", err, out)
	}
	var lookup struct {
		Data struct {
			Rows []map[string]any `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &lookup); err != nil || len(lookup.Data.Rows) != 1 {
		t.Fatalf("lookup envelope: %v\n%s", err, out)
	}
	check("lookup --json", lookup.Data.Rows[0])
}

// TestCliProcessRecordRows_RenderCalendarLiterals: `api process` record
// rows (an ungrouped windowed run) render date / datetime as calendar
// literals in text mode, the --json envelope and every --stream NDJSON
// row.
func TestCliProcessRecordRows_RenderCalendarLiterals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeReaderCohort(t, dir)
	reqPath := filepath.Join(dir, "req.json")
	req := `{"cohort":{"filename":"` + path + `"},"windows":[{"type":"WIN_ROW_NUMBER","label":"rn","order_by":[{"field":"d"}]}]}`
	if err := os.WriteFile(reqPath, []byte(req), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(surface string, row map[string]any) {
		t.Helper()
		if row["d"] != "2026-01-01" || row["ts"] != "2026-01-02T03:04:05Z" {
			t.Errorf("%s row d=%#v ts=%#v, want 2026-01-01 / 2026-01-02T03:04:05Z", surface, row["d"], row["ts"])
		}
	}

	out, err := runApp(t, "api", "process", "--request", reqPath)
	if err != nil {
		t.Fatalf("api process: %v\n%s", err, out)
	}
	var text struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &text); err != nil || len(text.Data) != 1 {
		t.Fatalf("process text: %v\n%s", err, out)
	}
	check("process", text.Data[0])

	out, err = runApp(t, "api", "process", "--request", reqPath, "--json")
	if err != nil {
		t.Fatalf("api process --json: %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			Data []map[string]any `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Data.Data) != 1 {
		t.Fatalf("process envelope: %v\n%s", err, out)
	}
	check("process --json", env.Data.Data[0])

	out, err = runApp(t, "api", "process", "--request", reqPath, "--stream")
	if err != nil {
		t.Fatalf("api process --stream: %v\n%s", err, out)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(out, "\n", 2)[0]), &row); err != nil {
		t.Fatalf("stream row: %v\n%s", err, out)
	}
	check("process --stream", row)
}
