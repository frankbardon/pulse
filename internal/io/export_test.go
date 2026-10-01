package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
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

// importThreeFieldFixture writes a small three-field cohort and
// returns the in-mem filesystem holding "test.pulse". Used by the
// projection tests so every case shares the same source schema.
func importThreeFieldFixture(t *testing.T) afero.Fs {
	t.Helper()
	rows := [][]string{
		{"10", "alpha", "US"},
		{"20", "beta", "GB"},
		{"30", "alpha", "US"},
	}
	reader := newMockReader([]string{"age", "name", "country"}, rows)
	fs := afero.NewMemMapFs()
	if _, err := NewImportJob(reader, "test.pulse").runOnFs(fs); err != nil {
		t.Fatalf("Import: %v", err)
	}
	return fs
}

// runOnFs is a tiny test helper wrapping ImportJob.Run.
func (j *ImportJob) runOnFs(fs afero.Fs) (*ImportReport, error) {
	j.FS = fs
	return j.Run(context.Background())
}

func TestExportJob_Includes_NilExportsAllFields(t *testing.T) {
	fs := importThreeFieldFixture(t)
	w := &collectWriter{}
	job := NewExportJob("test.pulse", w)
	job.FS = fs

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := []string{"age", "name", "country"}
	if !equalStrings(w.header, want) {
		t.Errorf("header = %v, want %v", w.header, want)
	}
	if len(w.rows) != 3 || len(w.rows[0]) != 3 {
		t.Errorf("rows shape = %dx%d, want 3x3", len(w.rows), len(w.rows[0]))
	}
}

func TestExportJob_Includes_SubsetProjects(t *testing.T) {
	fs := importThreeFieldFixture(t)
	w := &collectWriter{}
	job := NewExportJob("test.pulse", w)
	job.FS = fs
	job.Includes = []string{"country", "age"} // order ignored; schema order wins

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := []string{"age", "country"}
	if !equalStrings(w.header, want) {
		t.Errorf("header = %v, want %v (schema order, not flag order)", w.header, want)
	}
	if len(w.rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(w.rows))
	}
	for i, row := range w.rows {
		if len(row) != 2 {
			t.Errorf("row %d width = %d, want 2", i, len(row))
		}
	}
	// Spot-check first row contents: age=10 first, country=US second.
	if w.rows[0][0] != "10" || w.rows[0][1] != "US" {
		t.Errorf("row 0 = %v, want [10 US]", w.rows[0])
	}
}

func TestExportJob_Includes_UnknownFieldErrors(t *testing.T) {
	fs := importThreeFieldFixture(t)
	w := &collectWriter{}
	job := NewExportJob("test.pulse", w)
	job.FS = fs
	job.Includes = []string{"age", "missing"}

	_, err := job.Run(context.Background())
	if err == nil {
		t.Fatal("expected PULSE_EXPORT_FIELD_UNKNOWN, got nil")
	}
	if !errors.HasCode(err, errors.PULSE_EXPORT_FIELD_UNKNOWN) {
		t.Errorf("err code = %v, want PULSE_EXPORT_FIELD_UNKNOWN", err)
	}
	// Writer must not see any rows when validation rejects up-front.
	if len(w.rows) != 0 {
		t.Errorf("writer received %d rows on error path, want 0", len(w.rows))
	}
}

func TestExportJob_Includes_DuplicatesAreIdempotent(t *testing.T) {
	fs := importThreeFieldFixture(t)
	w := &collectWriter{}
	job := NewExportJob("test.pulse", w)
	job.FS = fs
	job.Includes = []string{"age", "age", "country"}

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := []string{"age", "country"}
	if !equalStrings(w.header, want) {
		t.Errorf("header = %v, want %v (duplicate include must dedupe)", w.header, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
