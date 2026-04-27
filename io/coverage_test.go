package io

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

func TestImportJob_NilFS(t *testing.T) {
	reader := newMockReader([]string{"a"}, [][]string{{"1"}})
	job := NewImportJob(reader, "test.pulse")
	// FS not set.
	_, err := job.Run(context.Background())
	if err == nil {
		t.Error("expected error for nil FS")
	}
}

func TestExportJob_NilFS(t *testing.T) {
	writer := &collectWriter{}
	job := NewExportJob("test.pulse", writer)
	// FS not set.
	_, err := job.Run(context.Background())
	if err == nil {
		t.Error("expected error for nil FS")
	}
}

func TestExportJob_Predict_NilFS(t *testing.T) {
	writer := &collectWriter{}
	job := NewExportJob("test.pulse", writer)
	_, err := job.Predict(context.Background())
	if err == nil {
		t.Error("expected error for nil FS")
	}
}

func TestExportJob_MissingFile(t *testing.T) {
	fs := afero.NewMemMapFs()
	writer := &collectWriter{}
	job := NewExportJob("missing.pulse", writer)
	job.FS = fs
	_, err := job.Run(context.Background())
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestImportJob_RowError(t *testing.T) {
	// Import with explicit schema where data doesn't match.
	rows := [][]string{
		{"10"},
		{"notanumber"}, // should cause a row error
		{"30"},
	}
	reader := newMockReader([]string{"val"}, rows)
	fs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "val", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		},
	}

	job := NewImportJob(reader, "test.pulse")
	job.FS = fs
	job.Schema = schema

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.RowErrors) == 0 {
		t.Error("expected row errors")
	}
	// The valid rows should still be imported.
	if report.RowsImported != 2 {
		t.Errorf("RowsImported = %d, want 2", report.RowsImported)
	}
}

func TestConvertJob_NoResetReader(t *testing.T) {
	// A reader that doesn't implement ResetReader.
	reader := &nonResetReader{
		columns: []string{"a"},
		rows:    [][]string{{"1"}},
	}
	writer := &collectWriter{}
	job := NewConvertJob(reader, writer)
	// No schema provided -> needs inference -> needs ResetReader.
	_, err := job.Run(context.Background())
	if err == nil {
		t.Error("expected error for non-ResetReader without schema")
	}
}

func TestConvertJob_Predict_NoResetReader(t *testing.T) {
	reader := &nonResetReader{
		columns: []string{"a"},
		rows:    [][]string{{"1"}},
	}
	writer := &collectWriter{}
	job := NewConvertJob(reader, writer)
	_, err := job.Predict(context.Background())
	if err == nil {
		t.Error("expected error for non-ResetReader without schema")
	}
}

func TestImportJob_Predict_NoResetReader(t *testing.T) {
	reader := &nonResetReader{
		columns: []string{"a"},
		rows:    [][]string{{"1"}},
	}
	job := NewImportJob(reader, "test.pulse")
	job.FS = afero.NewMemMapFs()
	_, err := job.Predict(context.Background())
	if err == nil {
		t.Error("expected error for non-ResetReader without schema")
	}
}

func TestErrStopIteration(t *testing.T) {
	err := ErrStopIteration()
	if err == nil {
		t.Error("ErrStopIteration() returned nil")
	}
}

func TestImportJob_Run_ContextCancel(t *testing.T) {
	rows := make([][]string, 1000)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("%d", i)}
	}
	reader := newMockReader([]string{"val"}, rows)
	fs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "val", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	job := NewImportJob(reader, "test.pulse")
	job.FS = fs
	job.Schema = schema

	_, err := job.Run(ctx)
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

// nonResetReader is a Reader that does NOT implement ResetReader.
type nonResetReader struct {
	columns []string
	rows    [][]string
	pos     int
	started bool
}

func (r *nonResetReader) ReadHeader() ([]string, error) {
	r.started = true
	return r.columns, nil
}

func (r *nonResetReader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for r.pos < len(r.rows) {
		if err := fn(r.rows[r.pos]); err != nil {
			return err
		}
		r.pos++
	}
	return nil
}

func (r *nonResetReader) Close() error { return nil }
