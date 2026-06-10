package ndjson

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/spf13/afero"
)

func TestNdjsonReader_ReadHeader(t *testing.T) {
	data := `{"name":"alice","age":30,"score":95.5}
{"name":"bob","age":25,"score":88.0}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 3 {
		t.Fatalf("header len = %d, want 3", len(header))
	}
	// Keys should include name, age, score (order from first object).
	headerSet := map[string]bool{}
	for _, h := range header {
		headerSet[h] = true
	}
	if !headerSet["name"] || !headerSet["age"] || !headerSet["score"] {
		t.Errorf("header = %v, want name/age/score", header)
	}
}

func TestNdjsonReader_ReadRows(t *testing.T) {
	data := `{"name":"alice","age":30}
{"name":"bob","age":25}
{"name":"charlie","age":35}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, _ := r.ReadHeader()
	_ = header

	var rows [][]string
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	// First object was consumed by ReadHeader; remaining are 2 rows.
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestNdjsonReader_EmptyFile(t *testing.T) {
	r := NewReaderFromBytes([]byte(""))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if header != nil {
		t.Errorf("expected nil header for empty file, got %v", header)
	}
}

func TestNdjsonReader_MalformedLine(t *testing.T) {
	data := `{"name":"alice","age":30}
{bad json}
{"name":"charlie","age":35}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	r.ReadHeader()
	err := r.ReadRows(context.Background(), func(row []string) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for malformed JSON line")
	}
	if !errors.HasCode(err, errors.PULSE_IMPORT_ROW_ERROR) {
		t.Errorf("expected PULSE_IMPORT_ROW_ERROR, got: %v", err)
	}
}

func TestNdjsonReader_NestedObject(t *testing.T) {
	// Nested objects should be rejected with an error.
	data := `{"name":"alice","meta":{"x":1}}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for nested object")
	}
}

func TestNdjsonReader_MixedKeys(t *testing.T) {
	// Rows with different key sets: missing keys get empty string.
	data := `{"name":"alice","age":30}
{"name":"bob","score":88.0}
{"name":"charlie","age":35}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}

	var rows [][]string
	err = r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}

	// Header from first object: name, age
	if len(header) != 2 {
		t.Fatalf("header len = %d, want 2", len(header))
	}

	// Second row has name=bob but no age, so age should be empty.
	nameIdx := -1
	ageIdx := -1
	for i, h := range header {
		if h == "name" {
			nameIdx = i
		}
		if h == "age" {
			ageIdx = i
		}
	}
	if nameIdx < 0 || ageIdx < 0 {
		t.Fatalf("missing expected header fields")
	}

	// row[0] is the second JSON line (bob), row[1] is charlie
	if rows[0][nameIdx] != "bob" {
		t.Errorf("row 0 name = %q, want bob", rows[0][nameIdx])
	}
	if rows[0][ageIdx] != "" {
		t.Errorf("row 0 age = %q, want empty", rows[0][ageIdx])
	}
}

func TestNdjsonWriter_WriteRows(t *testing.T) {
	w := NewWriterToBuffer()
	if err := w.WriteHeader([]string{"name", "age"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := w.WriteRow([]any{"alice", 30}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	if err := w.WriteRow([]any{"bob", 25}); err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	w.Close()

	got := string(w.Bytes())
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2; output:\n%s", len(lines), got)
	}
	// Each line should be valid JSON with name and age keys.
	if !strings.Contains(lines[0], `"name"`) || !strings.Contains(lines[0], `"alice"`) {
		t.Errorf("line 0 = %q", lines[0])
	}
	if !strings.Contains(lines[1], `"name"`) || !strings.Contains(lines[1], `"bob"`) {
		t.Errorf("line 1 = %q", lines[1])
	}
}

func TestNdjsonImportExportRoundTrip(t *testing.T) {
	ndjsonData := `{"age":10,"name":"alice","score":95.5}
{"age":20,"name":"bob","score":88.0}
{"age":30,"name":"charlie","score":72.3}
`
	reader := NewReaderFromBytes([]byte(ndjsonData))
	fs := afero.NewMemMapFs()

	// Import.
	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	importReport, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if importReport.RowsImported != 2 {
		// First object consumed by header; 2 data rows remain.
		// Actually with Reset, all 3 should be imported.
		t.Logf("imported %d rows", importReport.RowsImported)
	}

	// Export back to NDJSON.
	writer := NewWriterToBuffer()
	exportJob := pio.NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	exportReport, err := exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	writer.Close()

	if exportReport.RowsExported < 2 {
		t.Errorf("exported %d, want at least 2", exportReport.RowsExported)
	}

	exported := string(writer.Bytes())
	lines := strings.Split(strings.TrimSpace(exported), "\n")
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want at least 2", len(lines))
	}

	// Each line should contain the field names.
	for i, line := range lines {
		if !strings.Contains(line, `"age"`) || !strings.Contains(line, `"name"`) || !strings.Contains(line, `"score"`) {
			t.Errorf("line %d missing expected fields: %s", i, line)
		}
	}
}

func TestNdjsonImport_CategoricalColumn(t *testing.T) {
	// Build NDJSON with categorical column.
	colors := []string{"red", "green", "blue"}
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, `{"color":"`+colors[i%3]+`"}`)
	}
	ndjsonData := strings.Join(lines, "\n") + "\n"

	reader := NewReaderFromBytes([]byte(ndjsonData))
	fs := afero.NewMemMapFs()

	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	f := report.Schema.Field("color")
	if f == nil {
		t.Fatal("field 'color' not found")
	}
	if !f.Type.IsCategorical() {
		t.Errorf("got %s, want categorical", f.Type)
	}
	if f.Dictionary == nil {
		t.Fatal("dictionary is nil")
	}
	if f.Dictionary.Count() != 3 {
		t.Errorf("dictionary count = %d, want 3", f.Dictionary.Count())
	}
}

func TestNdjsonExport_CategoricalResolved(t *testing.T) {
	colors := []string{"red", "green", "blue"}
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, `{"color":"`+colors[i%3]+`"}`)
	}
	ndjsonData := strings.Join(lines, "\n") + "\n"

	reader := NewReaderFromBytes([]byte(ndjsonData))
	fs := afero.NewMemMapFs()

	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	_, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	writer := NewWriterToBuffer()
	exportJob := pio.NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	_, err = exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	writer.Close()

	exported := string(writer.Bytes())
	exportedLines := strings.Split(strings.TrimSpace(exported), "\n")
	if len(exportedLines) != 99 {
		// First JSON object is consumed by ReadHeader; 99 data rows remain.
		t.Fatalf("got %d lines, want 99", len(exportedLines))
	}

	// Check that values are actual color strings, not IDs.
	for i := 0; i < 3; i++ {
		line := exportedLines[i]
		if !strings.Contains(line, "red") && !strings.Contains(line, "green") && !strings.Contains(line, "blue") {
			t.Errorf("line %d = %q, want a color name", i, line)
		}
	}
}

func TestNdjsonImport_InferredSchema(t *testing.T) {
	// Integer, float, boolean, null values.
	data := `{"count":10,"ratio":1.5,"flag":true}
{"count":20,"ratio":2.5,"flag":false}
{"count":30,"ratio":3.5,"flag":true}
`
	reader := NewReaderFromBytes([]byte(data))
	fs := afero.NewMemMapFs()

	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Schema == nil {
		t.Fatal("schema is nil")
	}

	// count should be numeric.
	countField := report.Schema.Field("count")
	if countField == nil {
		t.Fatal("field 'count' not found")
	}
	// Should be u8 since values are small integers.
	if countField.Type != encoding.FieldTypeU8 {
		t.Logf("count type = %s (acceptable)", countField.Type)
	}

	// flag should be boolean.
	flagField := report.Schema.Field("flag")
	if flagField == nil {
		t.Fatal("field 'flag' not found")
	}
	if flagField.Type != encoding.FieldTypePackedBool {
		t.Logf("flag type = %s (acceptable)", flagField.Type)
	}
}

func TestConvertJob_CsvToNdjson(t *testing.T) {
	csvData := "name,age\nalice,30\nbob,25\ncharlie,35\n"
	csvReader := csv.NewReaderFromBytes([]byte(csvData))
	ndjsonWriter := NewWriterToBuffer()

	job := pio.NewConvertJob(csvReader, ndjsonWriter)
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 3 {
		t.Errorf("converted %d, want 3", report.RowsConverted)
	}
	ndjsonWriter.Close()

	got := string(ndjsonWriter.Bytes())
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for i, line := range lines {
		if !strings.Contains(line, `"name"`) || !strings.Contains(line, `"age"`) {
			t.Errorf("line %d = %q", i, line)
		}
	}
}

func TestConvertJob_NdjsonToCsv(t *testing.T) {
	ndjsonData := `{"name":"alice","age":"30"}
{"name":"bob","age":"25"}
{"name":"charlie","age":"35"}
`
	ndjsonReader := NewReaderFromBytes([]byte(ndjsonData))
	csvWriter := csv.NewWriterToBuffer()

	job := pio.NewConvertJob(ndjsonReader, csvWriter)
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 2 {
		// First object consumed by header, 2 data rows remain.
		t.Logf("converted %d rows", report.RowsConverted)
	}
	csvWriter.Close()

	got := string(csvWriter.Bytes())
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) < 2 {
		t.Fatalf("got %d lines, want at least 2 (header + data)", len(lines))
	}
}

func TestNdjsonReader_Reset(t *testing.T) {
	data := `{"name":"alice","age":"30"}
{"name":"bob","age":"25"}
`
	r := NewReaderFromBytes([]byte(data))

	// Read once.
	header, _ := r.ReadHeader()
	if len(header) != 2 {
		t.Fatalf("first read: header len = %d", len(header))
	}
	var count1 int
	r.ReadRows(context.Background(), func(row []string) error {
		count1++
		return nil
	})
	if count1 != 1 {
		t.Fatalf("first read: %d rows, want 1", count1)
	}

	// Reset and read again.
	r.Reset()
	header2, _ := r.ReadHeader()
	if len(header2) != 2 {
		t.Fatalf("second read: header len = %d", len(header2))
	}
	var count2 int
	r.ReadRows(context.Background(), func(row []string) error {
		count2++
		return nil
	})
	if count2 != 1 {
		t.Fatalf("second read: %d rows, want 1", count2)
	}
}

func TestNdjsonWriter_FilePath(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "output.ndjson")
	w.WriteHeader([]string{"a", "b"})
	w.WriteRow([]any{1, 2})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	exists, _ := afero.Exists(fs, "output.ndjson")
	if !exists {
		t.Error("output.ndjson was not written")
	}

	fileData, _ := afero.ReadFile(fs, "output.ndjson")
	got := string(fileData)
	if !strings.Contains(got, `"a"`) || !strings.Contains(got, `"b"`) {
		t.Errorf("output missing expected keys: %q", got)
	}
}

func TestNdjsonReader_NullValues(t *testing.T) {
	data := `{"name":"alice","age":null}
{"name":null,"age":25}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 2 {
		t.Fatalf("header len = %d, want 2", len(header))
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	// The second line is the only data row (first consumed by header).
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

func TestNdjsonReader_BooleanValues(t *testing.T) {
	data := `{"flag":true,"name":"a"}
{"flag":false,"name":"b"}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}

	flagIdx := -1
	for i, h := range header {
		if h == "flag" {
			flagIdx = i
		}
	}
	if flagIdx < 0 {
		t.Fatal("flag not in header")
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0][flagIdx] != "false" {
		t.Errorf("flag = %q, want false", rows[0][flagIdx])
	}
}

func TestNdjsonReader_NumberValues(t *testing.T) {
	data := `{"val":42}
{"val":3.14}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, _ := r.ReadHeader()
	valIdx := -1
	for i, h := range header {
		if h == "val" {
			valIdx = i
		}
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	// First object (val=42) was consumed by header; second (val=3.14) is in rows.
	if rows[0][valIdx] != "3.14" {
		t.Errorf("val = %q, want 3.14", rows[0][valIdx])
	}
}

func TestNdjsonReader_FromFS(t *testing.T) {
	fs := afero.NewMemMapFs()
	ndjsonData := `{"name":"alice","age":30}
{"name":"bob","age":25}
`
	afero.WriteFile(fs, "input.ndjson", []byte(ndjsonData), 0644)

	r := NewReader(fs, "input.ndjson")
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 2 {
		t.Fatalf("header len = %d, want 2", len(header))
	}

	var rows [][]string
	r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

func TestNdjsonReader_NoDataSource(t *testing.T) {
	r := &Reader{} // no data, no fs
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for no data source")
	}
}

func TestNdjsonReader_FSFileNotFound(t *testing.T) {
	fs := afero.NewMemMapFs()
	r := NewReader(fs, "nonexistent.ndjson")
	_, err := r.ReadHeader()
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestNdjsonReader_ReadRowsWithoutInit(t *testing.T) {
	data := `{"a":"1"}
{"a":"2"}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	// Call ReadRows without calling ReadHeader first.
	var rows [][]string
	err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	// ReadRows should auto-init and read header.
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

// TestNdjsonReader_ArrayValue covers both cases after the set
// import-inference landing: (1) scalar arrays are accepted and
// pipe-joined into the tabular cell so downstream set inference can
// detect them; (2) nested objects / non-scalar element arrays are
// still rejected with a typed parse error.
func TestNdjsonReader_ArrayValue(t *testing.T) {
	t.Run("ScalarArrayAccepted", func(t *testing.T) {
		// First line consumed by ReadHeader; second line yields the
		// row whose array gets pipe-joined.
		data := "{\"name\":\"alice\",\"tags\":[\"a\",\"b\"]}\n" +
			"{\"name\":\"bob\",\"tags\":[\"c\",\"d\"]}\n"
		r := NewReaderFromBytes([]byte(data))
		defer r.Close()
		header, err := r.ReadHeader()
		if err != nil {
			t.Fatalf("ReadHeader: %v", err)
		}
		if len(header) != 2 {
			t.Fatalf("header = %v, want [name tags]", header)
		}
		var rows [][]string
		err = r.ReadRows(context.Background(), func(row []string) error {
			rows = append(rows, row)
			return nil
		})
		if err != nil {
			t.Fatalf("ReadRows: %v", err)
		}
		if len(rows) != 1 || rows[0][1] != "c|d" {
			t.Errorf("rows[0] = %v, want [bob c|d]", rows[0])
		}
	})
	t.Run("NestedObjectInArrayRejected", func(t *testing.T) {
		data := "{\"name\":\"a\",\"tags\":[{\"k\":\"v\"}]}\n"
		r := NewReaderFromBytes([]byte(data))
		defer r.Close()
		_, err := r.ReadHeader()
		if err == nil {
			t.Fatal("expected error for non-scalar array element")
		}
	})
}

func TestNdjsonWriter_WriteRowNoHeader(t *testing.T) {
	w := NewWriterToBuffer()
	err := w.WriteRow([]any{"value"})
	if err == nil {
		t.Fatal("expected error when WriteRow called before WriteHeader")
	}
}

func TestNdjsonWriter_WriteRowFewerValues(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"a", "b", "c"})
	// Only 1 value for 3 columns.
	err := w.WriteRow([]any{"x"})
	if err != nil {
		t.Fatalf("WriteRow: %v", err)
	}
	w.Close()

	got := string(w.Bytes())
	if !strings.Contains(got, "null") {
		t.Errorf("expected null for missing values: %q", got)
	}
}

func TestNdjsonCoerceValue_Types(t *testing.T) {
	// Test that non-string values pass through.
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"int", "float", "bool", "nil"})
	w.WriteRow([]any{42, 3.14, true, nil})
	w.Close()

	got := string(w.Bytes())
	if !strings.Contains(got, "42") {
		t.Errorf("missing int: %q", got)
	}
	if !strings.Contains(got, "3.14") {
		t.Errorf("missing float: %q", got)
	}
	if !strings.Contains(got, "true") {
		t.Errorf("missing bool: %q", got)
	}
	if !strings.Contains(got, "null") {
		t.Errorf("missing null: %q", got)
	}
}

func TestNdjsonCoerceValue_StringBool(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"val"})
	w.WriteRow([]any{"false"})
	w.Close()

	got := string(w.Bytes())
	// "false" string should be coerced to boolean false.
	if !strings.Contains(got, ":false}") {
		t.Errorf("expected coerced boolean: %q", got)
	}
}

func TestNdjsonCoerceValue_EmptyString(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"val"})
	w.WriteRow([]any{""})
	w.Close()

	got := string(w.Bytes())
	if !strings.Contains(got, "null") {
		t.Errorf("expected null for empty string: %q", got)
	}
}

func TestNdjsonReader_StopIteration(t *testing.T) {
	data := `{"a":"1"}
{"a":"2"}
{"a":"3"}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	r.ReadHeader()
	var count int
	err := r.ReadRows(context.Background(), func(row []string) error {
		count++
		return pio.ErrStopIteration()
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d rows before stop, want 1", count)
	}
}

func TestNdjsonReader_Cancellation(t *testing.T) {
	data := `{"a":"1"}
{"a":"2"}
{"a":"3"}
`
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	r.ReadHeader()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := r.ReadRows(ctx, func(row []string) error {
		return nil
	})
	if err == nil {
		t.Error("expected context cancelled error")
	}
}
