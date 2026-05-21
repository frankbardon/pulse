package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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

func TestConvertJob_Includes_ProjectsOutputButKeepsFullSchemaIntermediate(t *testing.T) {
	rows := [][]string{
		{"10", "alpha", "US"},
		{"20", "beta", "GB"},
	}
	source := newMockReader([]string{"age", "name", "country"}, rows)
	target := &collectWriter{}
	fs := afero.NewMemMapFs()

	job := NewConvertJob(source, target)
	job.KeepPulseAt = "kept.pulse"
	job.FS = fs
	job.Includes = []string{"age", "country"}

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(target.header) != 2 || target.header[0] != "age" || target.header[1] != "country" {
		t.Fatalf("output header = %v, want [age country]", target.header)
	}
	for i, row := range target.rows {
		if len(row) != 2 {
			t.Errorf("output row %d width = %d, want 2", i, len(row))
		}
	}

	// Intermediate .pulse must still carry every source field.
	f, err := fs.Open("kept.pulse")
	if err != nil {
		t.Fatalf("open intermediate: %v", err)
	}
	defer f.Close()
	if err := encoding.ReadHeader(f); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(f)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if len(schema.Fields) != 3 {
		t.Errorf("intermediate schema fields = %d, want 3 (projection must not narrow on-disk schema)", len(schema.Fields))
	}
}

func TestConvertJob_Includes_UnknownField(t *testing.T) {
	rows := [][]string{{"10", "x"}}
	source := newMockReader([]string{"age", "name"}, rows)
	target := &collectWriter{}

	job := NewConvertJob(source, target)
	job.Includes = []string{"missing"}

	_, err := job.Run(context.Background())
	if err == nil {
		t.Fatal("expected PULSE_EXPORT_FIELD_UNKNOWN, got nil")
	}
	if !errors.HasCode(err, errors.PULSE_EXPORT_FIELD_UNKNOWN) {
		t.Errorf("err code = %v, want PULSE_EXPORT_FIELD_UNKNOWN", err)
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
