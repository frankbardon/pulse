package io

import (
	"context"
	"testing"

	"github.com/spf13/afero"
)

func TestConvertJob_CsvToTsv(t *testing.T) {
	rows := [][]string{
		{"10", "hello"},
		{"20", "world"},
		{"30", "hello"},
	}
	source := newMockReader([]string{"age", "name"}, rows)
	target := &collectWriter{}

	job := NewConvertJob(source, target)
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 3 {
		t.Errorf("RowsConverted = %d, want 3", report.RowsConverted)
	}
	if len(target.header) != 2 {
		t.Errorf("header = %v, want 2 columns", target.header)
	}
	if len(target.rows) != 3 {
		t.Errorf("rows = %d, want 3", len(target.rows))
	}
}

func TestConvertJob_KeepPulse(t *testing.T) {
	rows := [][]string{
		{"10", "hello"},
		{"20", "world"},
	}
	source := newMockReader([]string{"age", "name"}, rows)
	target := &collectWriter{}
	fs := afero.NewMemMapFs()

	job := NewConvertJob(source, target)
	job.KeepPulseAt = "intermediate.pulse"
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 2 {
		t.Errorf("RowsConverted = %d, want 2", report.RowsConverted)
	}

	// Check the intermediate pulse file was written.
	exists, _ := afero.Exists(fs, "intermediate.pulse")
	if !exists {
		t.Error("intermediate.pulse was not written")
	}
}

func TestConvertJob_Predict(t *testing.T) {
	rows := [][]string{
		{"10"}, {"20"}, {"30"}, {"40"}, {"50"},
	}
	source := newMockReader([]string{"val"}, rows)
	target := &collectWriter{}

	job := NewConvertJob(source, target)
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

	// Target should not have received any data.
	if len(target.rows) != 0 {
		t.Errorf("Predict should not write rows, got %d", len(target.rows))
	}
}
