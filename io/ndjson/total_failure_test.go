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
// of the total-failure verdict E3-S7 added to pio.ImportJob.Run against a
// live landmine.
//
// ndjson.Reader.ReadHeader derives the column names by consuming the FIRST
// JSON object and ReadRows resumes after it, so an INFERRED import of a
// one-record file sees zero data rows. That dropped record is a separate,
// pre-existing bug. What matters here is where the refusal comes from:
// inference already refuses a zero-row sample with
// PULSE_IMPORT_SCHEMA_AMBIGUOUS, long before the row pass runs, so the new
// check neither fires nor is needed. A PULSE_IMPORT_ROW_ERROR here would
// mean the verdict had swallowed an empty source.
func TestNdjsonImport_SingleRecordIsNotATotalRowFailure(t *testing.T) {
	data := []byte(`{"name":"alice","age":30}` + "\n")

	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(NewReaderFromBytes(data), "one.pulse")
	job.FS = fs

	_, err := job.Run(context.Background())
	if err == nil {
		t.Skip("the dropped-first-record bug appears fixed; this boundary no longer reproduces")
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("err = %v, want a CodedError", err)
	}
	if ce.Code == perr.PULSE_IMPORT_ROW_ERROR {
		t.Fatalf("the total-failure verdict fired on a source with no data rows and no row errors: %v", err)
	}
	if ce.Code != perr.PULSE_IMPORT_SCHEMA_AMBIGUOUS {
		t.Errorf("code = %s, want %s (inference refuses a zero-row sample)", ce.Code, perr.PULSE_IMPORT_SCHEMA_AMBIGUOUS)
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
