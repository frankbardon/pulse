package io

import (
	"context"
	"testing"

	"github.com/spf13/afero"
)

func TestExportJob_Run_EndToEnd(t *testing.T) {
	// First import some data.
	rows := [][]string{
		{"10", "hello"},
		{"20", "world"},
		{"30", "hello"},
	}
	reader := newMockReader([]string{"age", "name"}, rows)
	fs := afero.NewMemMapFs()

	importJob := NewImportJob(reader, "test.pulse")
	importJob.FS = fs

	_, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Now export.
	writer := &collectWriter{}
	exportJob := NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	report, err := exportJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if report.RowsExported != 3 {
		t.Errorf("RowsExported = %d, want 3", report.RowsExported)
	}

	// Verify header.
	if len(writer.header) != 2 {
		t.Fatalf("header len = %d, want 2", len(writer.header))
	}
	if writer.header[0] != "age" || writer.header[1] != "name" {
		t.Errorf("header = %v, want [age, name]", writer.header)
	}

	// Verify row count.
	if len(writer.rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(writer.rows))
	}
}

func TestExportJob_Predict(t *testing.T) {
	// Import first.
	rows := [][]string{
		{"10"}, {"20"}, {"30"},
	}
	reader := newMockReader([]string{"val"}, rows)
	fs := afero.NewMemMapFs()

	importJob := NewImportJob(reader, "test.pulse")
	importJob.FS = fs
	_, err := importJob.Run(context.Background())
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Predict export.
	writer := &collectWriter{}
	exportJob := NewExportJob("test.pulse", writer)
	exportJob.FS = fs

	report, err := exportJob.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if report.Schema == nil {
		t.Fatal("Schema is nil")
	}
	if report.EstimatedRows != 3 {
		t.Errorf("EstimatedRows = %d, want 3", report.EstimatedRows)
	}

	// Writer should not have received any data.
	if len(writer.rows) != 0 {
		t.Errorf("Predict should not write rows, got %d", len(writer.rows))
	}
}

// collectWriter is a Writer that collects output in memory for testing.
type collectWriter struct {
	header []string
	rows   [][]any
}

func (w *collectWriter) WriteHeader(columns []string) error {
	w.header = columns
	return nil
}

func (w *collectWriter) WriteRow(values []any) error {
	row := make([]any, len(values))
	copy(row, values)
	w.rows = append(w.rows, row)
	return nil
}

func (w *collectWriter) Close() error { return nil }
