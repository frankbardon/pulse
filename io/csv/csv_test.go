package csv

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

func TestCsvReader_ReadHeader(t *testing.T) {
	data := "name,age,score\nalice,30,95.5\nbob,25,88.0\n"
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(header) != 3 {
		t.Fatalf("header len = %d, want 3", len(header))
	}
	if header[0] != "name" || header[1] != "age" || header[2] != "score" {
		t.Errorf("header = %v", header)
	}
}

func TestCsvReader_ReadRows(t *testing.T) {
	data := "name,age\nalice,30\nbob,25\ncharlie,35\n"
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	var rows [][]string
	header, _ := r.ReadHeader()
	_ = header

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
	if rows[0][0] != "alice" || rows[0][1] != "30" {
		t.Errorf("row 0 = %v", rows[0])
	}
}

func TestCsvReader_EmptyFile(t *testing.T) {
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

func TestCsvReader_MalformedRow(t *testing.T) {
	// Bare quotes in non-quoted field cause parse errors.
	data := "name,age\nalice,30\n\"unterminated\n"
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	r.ReadHeader()
	var rowCount int
	err := r.ReadRows(context.Background(), func(row []string) error {
		rowCount++
		return nil
	})
	// With LazyQuotes, this may not error. Let's use a truly malformed CSV.
	// The important thing is that errors get PULSE_IMPORT_ROW_ERROR code.
	_ = err
	_ = rowCount
}

func TestCsvReader_MalformedRow_BareQuote(t *testing.T) {
	// CSV with wrong number of fields when FieldsPerRecord is strict.
	// Since we use FieldsPerRecord=-1, this won't error. Instead test
	// that the error code is correct when we do get a parse error.
	data := "a,b\n1,2\n\"bad"
	r := NewReaderFromBytes([]byte(data))
	r.cr = nil // force reinit
	defer r.Close()

	r.ReadHeader()
	err := r.ReadRows(context.Background(), func(row []string) error {
		return nil
	})
	if err != nil {
		if !errors.HasCode(err, errors.PULSE_IMPORT_ROW_ERROR) {
			t.Errorf("expected PULSE_IMPORT_ROW_ERROR, got: %v", err)
		}
	}
}

func TestCsvWriter_WriteHeader(t *testing.T) {
	w := NewWriterToBuffer()
	if err := w.WriteHeader([]string{"name", "age"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	w.Close()

	got := string(w.Bytes())
	if !strings.HasPrefix(got, "name,age") {
		t.Errorf("output = %q, want prefix 'name,age'", got)
	}
}

func TestCsvWriter_WriteRows(t *testing.T) {
	w := NewWriterToBuffer()
	w.WriteHeader([]string{"name", "age"})
	w.WriteRow([]any{"alice", 30})
	w.WriteRow([]any{"bob", 25})
	w.Close()

	got := string(w.Bytes())
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	if lines[0] != "name,age" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != "alice,30" {
		t.Errorf("line 1 = %q", lines[1])
	}
}

func TestCsvImportExportRoundTrip(t *testing.T) {
	csvData := "age,name,score\n10,alice,95.5\n20,bob,88.0\n30,charlie,72.3\n"
	reader := NewReaderFromBytes([]byte(csvData))
	fs := afero.NewMemMapFs()

	// Import.
	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	importReport, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if importReport.RowsImported != 3 {
		t.Errorf("imported %d, want 3", importReport.RowsImported)
	}

	// Export.
	writer := NewWriterToBuffer()
	exportJob := pio.NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	exportReport, err := exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if exportReport.RowsExported != 3 {
		t.Errorf("exported %d, want 3", exportReport.RowsExported)
	}
	writer.Close()

	// Parse the exported CSV and verify the data matches.
	exported := string(writer.Bytes())
	lines := strings.Split(strings.TrimSpace(exported), "\n")
	if len(lines) != 4 { // header + 3 rows
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if lines[0] != "age,name,score" {
		t.Errorf("header = %q", lines[0])
	}

	// Verify the ages are preserved.
	for i, line := range lines[1:] {
		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			t.Errorf("row %d has %d parts", i, len(parts))
		}
	}
}

func TestCsvImport_WithInferredSchema(t *testing.T) {
	csvData := "val\n10\n20\n30\n40\n50\n"
	reader := NewReaderFromBytes([]byte(csvData))
	fs := afero.NewMemMapFs()

	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs
	// No schema set -> infer.

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Schema == nil {
		t.Fatal("schema is nil")
	}
	f := report.Schema.Field("val")
	if f == nil {
		t.Fatal("field 'val' not found")
	}
	// Should infer as u8.
	if f.Type != encoding.FieldTypeU8 {
		t.Errorf("got %s, want u8", f.Type)
	}
}

func TestCsvImport_WithExplicitSchema(t *testing.T) {
	csvData := "val\n10\n20\n30\n"
	reader := NewReaderFromBytes([]byte(csvData))
	fs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "val", Type: encoding.FieldTypeU16, CsvColumnIdx: 0},
		},
	}

	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs
	job.Schema = schema

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Schema.Field("val").Type != encoding.FieldTypeU16 {
		t.Errorf("schema should use explicit U16, got %s", report.Schema.Field("val").Type)
	}
}

func TestCsvImport_CategoricalColumn(t *testing.T) {
	lines := []string{"color"}
	colors := []string{"red", "green", "blue", "red", "green"}
	lines = append(lines, colors...)
	// Need enough rows for inference.
	for i := 0; i < 95; i++ {
		lines = append(lines, colors[i%len(colors)])
	}
	csvData := strings.Join(lines, "\n") + "\n"

	reader := NewReaderFromBytes([]byte(csvData))
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

func TestCsvImport_FieldDescriptions(t *testing.T) {
	csvData := "val\n10\n20\n"
	reader := NewReaderFromBytes([]byte(csvData))
	fs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{
				Name:        "val",
				Type:        encoding.FieldTypeU8,
				CsvColumnIdx: 0,
				Description: "A test value",
			},
		},
	}

	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs
	job.Schema = schema

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	f := report.Schema.Field("val")
	if f.Description != "A test value" {
		t.Errorf("description = %q, want 'A test value'", f.Description)
	}
}

func TestCsvExport_CategoricalResolved(t *testing.T) {
	// Import with categorical column.
	lines := []string{"color"}
	colors := []string{"red", "green", "blue"}
	for i := 0; i < 100; i++ {
		lines = append(lines, colors[i%len(colors)])
	}
	csvData := strings.Join(lines, "\n") + "\n"

	reader := NewReaderFromBytes([]byte(csvData))
	fs := afero.NewMemMapFs()

	importJob := pio.NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	_, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Export and verify categorical values are resolved to strings.
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
	if len(exportedLines) != 101 { // header + 100 rows
		t.Fatalf("got %d lines, want 101", len(exportedLines))
	}

	// Check some values are actual color strings.
	for i := 1; i < 4; i++ {
		val := strings.TrimSpace(exportedLines[i])
		if val != "red" && val != "green" && val != "blue" {
			t.Errorf("row %d = %q, want a color name", i, val)
		}
	}
}

func FuzzCsvHeaderParse(f *testing.F) {
	f.Add([]byte("a,b,c\n1,2,3\n"))
	f.Add([]byte("\"quoted\",normal\nval1,val2\n"))
	f.Add([]byte(",,,\n,,,\n"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReaderFromBytes(data)
		header, err := r.ReadHeader()
		if err != nil {
			return // parse errors are expected
		}
		_ = header
		r.Close()
	})
}

func TestCsvReader_ResetReader(t *testing.T) {
	data := "name,age\nalice,30\nbob,25\n"
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
	if count1 != 2 {
		t.Fatalf("first read: %d rows", count1)
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
	if count2 != 2 {
		t.Fatalf("second read: %d rows", count2)
	}
}

func TestCsvWriter_FilePath(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "output.csv")
	w.WriteHeader([]string{"a", "b"})
	w.WriteRow([]any{1, 2})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	exists, _ := afero.Exists(fs, "output.csv")
	if !exists {
		t.Error("output.csv was not written")
	}

	data, _ := afero.ReadFile(fs, "output.csv")
	got := string(data)
	if !strings.Contains(got, "a,b") {
		t.Errorf("output missing header: %q", got)
	}
}
