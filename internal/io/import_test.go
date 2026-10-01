package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

func TestImportJob_Run_EndToEnd(t *testing.T) {
	rows := [][]string{
		{"10", "hello", "true"},
		{"20", "world", "false"},
		{"30", "hello", "true"},
	}
	reader := newMockReader([]string{"age", "name", "active"}, rows)
	fs := afero.NewMemMapFs()

	job := NewImportJob(reader, "test.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsImported != 3 {
		t.Errorf("RowsImported = %d, want 3", report.RowsImported)
	}
	if report.Schema == nil {
		t.Fatal("Schema is nil")
	}
	if len(report.Schema.Fields) != 3 {
		t.Errorf("Fields = %d, want 3", len(report.Schema.Fields))
	}

	// Verify the file was written.
	exists, _ := afero.Exists(fs, "test.pulse")
	if !exists {
		t.Error("test.pulse was not written")
	}
}

func TestImportJob_WithExplicitSchema(t *testing.T) {
	rows := [][]string{
		{"10", "hello"},
		{"20", "world"},
	}
	reader := newMockReader([]string{"age", "name"}, rows)
	fs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "age", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
			{Name: "name", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
		},
	}

	job := NewImportJob(reader, "test.pulse")
	job.FS = fs
	job.Schema = schema

	// With explicit schema, we don't need ReadHeader to be called again for inference.
	// The reader just needs to stream rows.
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsImported != 2 {
		t.Errorf("RowsImported = %d, want 2", report.RowsImported)
	}
}

func TestImportJob_Predict(t *testing.T) {
	rows := [][]string{
		{"10"}, {"20"}, {"30"}, {"40"}, {"50"},
	}
	reader := newMockReader([]string{"val"}, rows)

	job := NewImportJob(reader, "test.pulse")
	job.FS = afero.NewMemMapFs()

	report, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if report.Schema == nil {
		t.Fatal("Schema is nil")
	}
	if report.EstimatedRows != 5 {
		t.Errorf("EstimatedRows = %d, want 5", report.EstimatedRows)
	}

	// Verify no file was written.
	exists, _ := afero.Exists(job.FS, "test.pulse")
	if exists {
		t.Error("Predict should not write a file")
	}
}

func TestImportJob_StreamingMemory(t *testing.T) {
	// Verify that even with many rows, the import completes without error.
	// This is a basic smoke test for streaming behavior.
	rows := make([][]string, 10000)
	for i := range rows {
		rows[i] = []string{"42"}
	}
	reader := newMockReader([]string{"val"}, rows)
	fs := afero.NewMemMapFs()

	job := NewImportJob(reader, "test.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsImported != 10000 {
		t.Errorf("RowsImported = %d, want 10000", report.RowsImported)
	}
}
