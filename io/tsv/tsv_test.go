package tsv

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

func TestTsvReader_ReadHeader(t *testing.T) {
	data := "name\tage\tscore\nalice\t30\t95.5\nbob\t25\t88.0\n"
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

func TestTsvReader_ReadRows(t *testing.T) {
	data := "name\tage\nalice\t30\nbob\t25\ncharlie\t35\n"
	r := NewReaderFromBytes([]byte(data))
	defer r.Close()

	r.ReadHeader()

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
	if rows[0][0] != "alice" || rows[0][1] != "30" {
		t.Errorf("row 0 = %v", rows[0])
	}
}

func TestTsvReader_EmptyFile(t *testing.T) {
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

func TestTsvWriter_WriteHeader(t *testing.T) {
	w := NewWriterToBuffer()
	if err := w.WriteHeader([]string{"name", "age"}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	w.Close()

	got := string(w.Bytes())
	if !strings.HasPrefix(got, "name\tage") {
		t.Errorf("output = %q, want prefix 'name\\tage'", got)
	}
}

func TestTsvWriter_WriteRows(t *testing.T) {
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
	if lines[0] != "name\tage" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != "alice\t30" {
		t.Errorf("line 1 = %q", lines[1])
	}
}

func TestTsvImportExportRoundTrip(t *testing.T) {
	tsvData := "age\tname\tscore\n10\talice\t95.5\n20\tbob\t88.0\n30\tcharlie\t72.3\n"
	reader := NewReaderFromBytes([]byte(tsvData))
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

	// Parse the exported TSV.
	exported := string(writer.Bytes())
	lines := strings.Split(strings.TrimSpace(exported), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
	if lines[0] != "age\tname\tscore" {
		t.Errorf("header = %q", lines[0])
	}
}

func TestTsvImport_WithInferredSchema(t *testing.T) {
	tsvData := "val\n10\n20\n30\n40\n50\n"
	reader := NewReaderFromBytes([]byte(tsvData))
	fs := afero.NewMemMapFs()

	job := pio.NewImportJob(reader, "test.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	f := report.Schema.Field("val")
	if f == nil {
		t.Fatal("field 'val' not found")
	}
	if f.Type != encoding.FieldTypeU8 {
		t.Errorf("got %s, want u8", f.Type)
	}
}

func TestTsvReader_ResetReader(t *testing.T) {
	data := "name\tage\nalice\t30\nbob\t25\n"
	r := NewReaderFromBytes([]byte(data))

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

func TestTsvWriter_FilePath(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "output.tsv")
	w.WriteHeader([]string{"a", "b"})
	w.WriteRow([]any{1, 2})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	exists, _ := afero.Exists(fs, "output.tsv")
	if !exists {
		t.Error("output.tsv was not written")
	}

	data, _ := afero.ReadFile(fs, "output.tsv")
	got := string(data)
	if !strings.Contains(got, "a\tb") {
		t.Errorf("output missing header: %q", got)
	}
}

func TestTsvImport_CategoricalColumn(t *testing.T) {
	lines := []string{"color"}
	colors := []string{"red", "green", "blue"}
	for i := 0; i < 100; i++ {
		lines = append(lines, colors[i%len(colors)])
	}
	tsvData := strings.Join(lines, "\n") + "\n"

	reader := NewReaderFromBytes([]byte(tsvData))
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
}

func FuzzTsvHeaderParse(f *testing.F) {
	f.Add([]byte("a\tb\tc\n1\t2\t3\n"))
	f.Add([]byte(""))
	f.Add([]byte("\t\t\n\t\t\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReaderFromBytes(data)
		header, err := r.ReadHeader()
		if err != nil {
			return
		}
		_ = header
		r.Close()
	})
}
