package jsonarray

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/spf13/afero"
)

func TestReader_ReadHeader(t *testing.T) {
	data := `[{"name":"alice","age":30,"score":95.5},{"name":"bob","age":25,"score":88.0}]`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 3 {
		t.Fatalf("header len = %d, want 3", len(header))
	}
	want := []string{"name", "age", "score"}
	for i, h := range want {
		if header[i] != h {
			t.Errorf("header[%d] = %q, want %q", i, header[i], h)
		}
	}
}

func TestReader_ReadRows(t *testing.T) {
	data := `[{"name":"alice","age":30},{"name":"bob","age":25},{"name":"charlie","age":35}]`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}

	var rows [][]string
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	if rows[0][0] != "alice" || rows[1][0] != "bob" || rows[2][0] != "charlie" {
		t.Errorf("rows = %v", rows)
	}
}

func TestReader_EmptyArray(t *testing.T) {
	r := NewReaderFromBytes([]byte(`[]`))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if header != nil {
		t.Errorf("expected nil header for empty array, got %v", header)
	}

	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestReader_EmptyInput(t *testing.T) {
	r := NewReaderFromBytes([]byte(``))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if header != nil {
		t.Errorf("expected nil header, got %v", header)
	}
}

func TestReader_NotAnArray(t *testing.T) {
	r := NewReaderFromBytes([]byte(`{"name":"alice"}`))
	defer r.Close()
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for non-array top level")
	}
}

func TestReader_MalformedElement(t *testing.T) {
	data := `[{"name":"alice"},{bad}]`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	err := r.ReadRows(context.Background(), func(row []string) error { return nil })
	if err == nil {
		t.Fatal("expected error for malformed element")
	}
	if !errors.HasCode(err, errors.PULSE_IMPORT_ROW_ERROR) {
		t.Errorf("expected PULSE_IMPORT_ROW_ERROR, got: %v", err)
	}
}

func TestReader_NestedObject(t *testing.T) {
	r := NewReaderFromBytes([]byte(`[{"name":"a","meta":{"x":1}}]`))
	defer r.Close()
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for nested object")
	}
}

func TestReader_ArrayValue(t *testing.T) {
	r := NewReaderFromBytes([]byte(`[{"name":"a","tags":["x","y"]}]`))
	defer r.Close()
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for array-valued field")
	}
}

func TestReader_MixedKeys(t *testing.T) {
	data := `[{"name":"alice","age":30},{"name":"bob","score":88.0},{"name":"charlie","age":35}]`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 2 {
		t.Fatalf("header = %v, want [name age]", header)
	}

	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	// row index 1 is bob: name=bob, age=""
	if rows[1][0] != "bob" || rows[1][1] != "" {
		t.Errorf("bob row = %v, want [bob \"\"]", rows[1])
	}
}

func TestReader_PrettyPrintedInput(t *testing.T) {
	data := `[
  {
    "name": "alice",
    "age": 30
  },
  {
    "name": "bob",
    "age": 25
  }
]`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestReader_NumberAndBoolAndNull(t *testing.T) {
	data := `[{"i":42,"f":3.14,"b":true,"n":null}]`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0][idx["i"]] != "42" || rows[0][idx["f"]] != "3.14" {
		t.Errorf("number values = %v", rows[0])
	}
	if rows[0][idx["b"]] != "true" {
		t.Errorf("bool = %q", rows[0][idx["b"]])
	}
	if rows[0][idx["n"]] != "" {
		t.Errorf("null = %q, want empty", rows[0][idx["n"]])
	}
}

func TestReader_Reset(t *testing.T) {
	data := `[{"a":"1"},{"a":"2"}]`
	r := NewReaderFromBytes([]byte(data))

	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	var c1 int
	r.ReadRows(context.Background(), func(row []string) error { c1++; return nil })
	if c1 != 2 {
		t.Fatalf("first: %d rows, want 2", c1)
	}

	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := r.ReadHeader(); err != nil {
		t.Fatalf("ReadHeader after reset: %v", err)
	}
	var c2 int
	r.ReadRows(context.Background(), func(row []string) error { c2++; return nil })
	if c2 != 2 {
		t.Fatalf("second: %d rows, want 2", c2)
	}
}

func TestReader_StopIteration(t *testing.T) {
	r := NewReaderFromBytes([]byte(`[{"a":"1"},{"a":"2"},{"a":"3"}]`))
	defer r.Close()
	r.ReadHeader()
	var n int
	err := r.ReadRows(context.Background(), func(row []string) error {
		n++
		return pio.ErrStopIteration()
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if n != 1 {
		t.Errorf("got %d before stop, want 1", n)
	}
}

func TestReader_Cancellation(t *testing.T) {
	r := NewReaderFromBytes([]byte(`[{"a":"1"},{"a":"2"},{"a":"3"}]`))
	defer r.Close()
	r.ReadHeader()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.ReadRows(ctx, func(row []string) error { return nil })
	if err == nil {
		t.Error("expected context cancelled")
	}
}

func TestReader_FromFS(t *testing.T) {
	fs := afero.NewMemMapFs()
	afero.WriteFile(fs, "input.json", []byte(`[{"name":"alice","age":30},{"name":"bob","age":25}]`), 0644)

	r := NewReader(fs, "input.json")
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 2 {
		t.Fatalf("header len = %d, want 2", len(header))
	}
	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error { rows = append(rows, row); return nil })
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestReader_NoDataSource(t *testing.T) {
	r := &Reader{}
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReader_FileNotFound(t *testing.T) {
	r := NewReader(afero.NewMemMapFs(), "missing.json")
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriter_RoundTrip(t *testing.T) {
	w := NewWriterToBuffer()
	if err := w.WriteHeader([]string{"name", "age"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	w.WriteRow([]any{"alice", 30})
	w.WriteRow([]any{"bob", 25})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := w.Bytes()
	var parsed []map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, got)
	}
	if len(parsed) != 2 {
		t.Fatalf("parsed %d objects, want 2", len(parsed))
	}
	if parsed[0]["name"] != "alice" || parsed[1]["name"] != "bob" {
		t.Errorf("parsed = %v", parsed)
	}
}

func TestWriter_EmptyArray(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"a"})
	w.Close()
	got := string(w.Bytes())
	if got != `[]` {
		t.Errorf("empty got = %q, want []", got)
	}
}

func TestWriter_NoHeader(t *testing.T) {
	w := NewWriterToBuffer()
	err := w.WriteRow([]any{"x"})
	if err == nil {
		t.Fatal("expected error when WriteRow before WriteHeader")
	}
}

func TestWriter_WriteRowFewerValues(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"a", "b", "c"})
	w.WriteRow([]any{"x"})
	w.Close()
	got := string(w.Bytes())
	if !strings.Contains(got, "null") {
		t.Errorf("expected null padding: %q", got)
	}
}

func TestWriter_FilePath(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "out.json")
	w.WriteHeader([]string{"a", "b"})
	w.WriteRow([]any{1, 2})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	exists, _ := afero.Exists(fs, "out.json")
	if !exists {
		t.Error("out.json not written")
	}
	data, _ := afero.ReadFile(fs, "out.json")
	var parsed []map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("file not valid JSON: %v", err)
	}
}

func TestImportExportRoundTrip(t *testing.T) {
	src := `[{"age":10,"name":"alice","score":95.5},{"age":20,"name":"bob","score":88.0},{"age":30,"name":"charlie","score":72.3}]`

	reader := NewReaderFromBytes([]byte(src))
	fs := afero.NewMemMapFs()

	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs
	if _, err := importJob.Run(context.Background()); err != nil {
		t.Fatalf("Import: %v", err)
	}

	writer := NewWriterToBuffer()
	exportJob := pio.NewExportJob("test.pulse", writer)
	exportJob.FS = fs
	if _, err := exportJob.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var parsed []map[string]any
	if err := json.Unmarshal(writer.Bytes(), &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, writer.Bytes())
	}
	if len(parsed) < 2 {
		t.Fatalf("parsed %d, want at least 2", len(parsed))
	}
	for i, obj := range parsed {
		if _, ok := obj["name"]; !ok {
			t.Errorf("object %d missing name: %v", i, obj)
		}
	}
}

func TestImport_CategoricalColumn(t *testing.T) {
	colors := []string{"red", "green", "blue"}
	objs := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		objs = append(objs, `{"color":"`+colors[i%3]+`"}`)
	}
	src := `[` + strings.Join(objs, ",") + `]`

	reader := NewReaderFromBytes([]byte(src))
	fs := afero.NewMemMapFs()

	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	f := report.Schema.Field("color")
	if f == nil {
		t.Fatal("color field missing")
	}
	if !f.Type.IsCategorical() {
		t.Errorf("type = %s, want categorical", f.Type)
	}
	if f.Dictionary == nil || f.Dictionary.Count() != 3 {
		t.Errorf("dictionary = %v", f.Dictionary)
	}
}

func TestConvert_CsvToJsonArray(t *testing.T) {
	csvData := "name,age\nalice,30\nbob,25\ncharlie,35\n"
	csvReader := csv.NewReaderFromBytes([]byte(csvData))
	jw := NewWriterToBuffer()

	job := pio.NewConvertJob(csvReader, jw)
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 3 {
		t.Errorf("converted %d, want 3", report.RowsConverted)
	}
	if err := jw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var parsed []map[string]any
	if err := json.Unmarshal(jw.Bytes(), &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, jw.Bytes())
	}
	if len(parsed) != 3 {
		t.Fatalf("got %d objects, want 3", len(parsed))
	}
}
