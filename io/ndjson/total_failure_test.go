package ndjson

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// TestNdjsonImport_SingleRecordIsNotATotalRowFailure guards the boundary
// of the total-failure verdict E3-S7 added to pio.ImportJob.Run.
//
// The shape was written when ndjson.Reader.ReadHeader still ATE the first
// object: a one-record file then presented zero data rows, inference
// refused the empty sample with PULSE_IMPORT_SCHEMA_AMBIGUOUS, and the
// only thing worth asserting was that the new verdict had not turned
// that into a PULSE_IMPORT_ROW_ERROR. The dropped record is fixed, so the
// boundary now has a real answer: one record in, one row imported. The
// verdict must fire on neither count — it is not a total row failure and
// it is no longer an ambiguous sample, because there IS a row to sample.
func TestNdjsonImport_SingleRecordIsNotATotalRowFailure(t *testing.T) {
	data := []byte(`{"name":"alice","age":30}` + "\n")

	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(NewReaderFromBytes(data), "one.pulse")
	job.FS = fs

	rep, err := job.Run(context.Background())
	if err != nil {
		var ce *perr.CodedError
		if stderrors.As(err, &ce) {
			t.Fatalf("Run: %s: %v — a one-record source imports one row", ce.Code, err)
		}
		t.Fatalf("Run: %v — a one-record source imports one row", err)
	}
	if rep.RowsImported != 1 {
		t.Fatalf("RowsImported = %d, want 1 (errors %v)", rep.RowsImported, rep.RowErrors)
	}
	if len(rep.RowErrors) != 0 {
		t.Errorf("RowErrors = %v, want none", rep.RowErrors)
	}
	if ok, _ := afero.Exists(fs, "one.pulse"); !ok {
		t.Error("one.pulse was not written")
	}
}

// TestNdjsonImport_ExplicitSchemaEmptySourceStaysLegitimate is the same
// boundary reached past inference. An explicit schema skips the sampling
// pass entirely, so a source with no data rows reaches the row pass with
// rowsImported 0 and no row errors — the one shape totalRowFailure must
// let through. It must produce an empty cohort, not an error.
func TestNdjsonImport_ExplicitSchemaEmptySourceStaysLegitimate(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(NewReaderFromBytes([]byte("")), "empty.pulse")
	job.FS = fs
	job.Schema = &encoding.Schema{Fields: []encoding.Field{
		{Name: "age", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
	}}

	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v — an empty source is an empty cohort, not a total failure", err)
	}
	if rep.RowsImported != 0 || len(rep.RowErrors) != 0 {
		t.Fatalf("RowsImported = %d, RowErrors = %v; want 0 and none", rep.RowsImported, rep.RowErrors)
	}
	if ok, _ := afero.Exists(fs, "empty.pulse"); !ok {
		t.Error("empty.pulse was not written; an empty cohort is still a cohort")
	}
}
