package io

import (
	"context"
	stderrors "errors"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// overflowSchema builds a one-column categorical_u8 schema. The rung holds
// 256 entries, so the 257th distinct value cannot be represented.
func overflowSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{{
		Name:         "answer",
		Type:         encoding.FieldTypeCategoricalU8,
		CsvColumnIdx: 0,
		Dictionary:   encoding.NewDictionary(),
	}}}
}

// asCodedConvertErr unwraps a coded error or fails the test.
func asCodedConvertErr(t *testing.T, err error) *perrors.CodedError {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error is not a *errors.CodedError: %T %v", err, err)
	}
	return ce
}

// distinctRows returns n rows whose single cell is distinct per row.
func distinctRows(n int) [][]string {
	rows := make([][]string, n)
	for i := range rows {
		rows[i] = []string{"v" + strconv.Itoa(i)}
	}
	return rows
}

// TestConvertJob_CategoricalOverflowIsReported is the regression for a
// dictionary-overflow error convert used to drop on the floor.
//
// ConvertJob.Run built a dictionary per categorical field and called
// AddWithLimit WITHOUT reading the return. Past the rung's capacity every
// further distinct value was discarded: the convert reported success, the
// ConvertReport carried a schema whose dictionary was silently short of the
// categories the data actually holds, and — with KeepPulseAt set — the
// intermediate re-import hit the same wall per row and dropped those rows
// from the cohort, its report discarded by convert.
//
// A capacity violation is not a per-row data condition. Once the rung is
// full, every subsequent unseen category is lost for the REST of the file,
// so there is no partial answer worth reporting: Run refuses with the code
// AddWithLimit already raises.
func TestConvertJob_CategoricalOverflowIsReported(t *testing.T) {
	source := newMockReader([]string{"answer"}, distinctRows(300))
	target := &collectWriter{}

	job := NewConvertJob(source, target)
	job.Schema = overflowSchema(t)

	report, err := job.Run(context.Background())
	if err == nil {
		t.Fatalf("Run succeeded on a categorical dictionary overflow; report = %+v", report)
	}
	if report != nil {
		t.Errorf("report = %+v on a refused convert, want nil", report)
	}

	ce := asCodedConvertErr(t, err)
	if ce.Code != perrors.PULSE_IMPORT_CATEGORICAL_OVERFLOW {
		t.Errorf("code = %q, want %q", ce.Code, perrors.PULSE_IMPORT_CATEGORICAL_OVERFLOW)
	}
	if got := ce.Details["column"]; got != "answer" {
		t.Errorf("details[column] = %v, want \"answer\"", got)
	}
	if got := ce.Details["max_entries"]; got != uint32(256) {
		t.Errorf("details[max_entries] = %v, want 256", got)
	}
	if _, ok := ce.Details["row"]; !ok {
		t.Errorf("details missing row: %v", ce.Details)
	}
}

// TestConvertJob_CategoricalOverflowLeavesNoCohort pins the ordering: the
// refusal must land BEFORE the KeepPulseAt intermediate is written, for the
// same reason the total-row-failure refusal does. A cohort on disk under a
// command that errored is the silent half of this bug.
func TestConvertJob_CategoricalOverflowLeavesNoCohort(t *testing.T) {
	source := newMockReader([]string{"answer"}, distinctRows(300))
	target := &collectWriter{}
	fs := afero.NewMemMapFs()

	job := NewConvertJob(source, target)
	job.Schema = overflowSchema(t)
	job.KeepPulseAt = "kept.pulse"
	job.FS = fs

	if _, err := job.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded on a categorical dictionary overflow")
	}
	if ok, _ := afero.Exists(fs, "kept.pulse"); ok {
		t.Error("kept.pulse was written under a refused convert")
	}
}

// TestConvertJob_CategoricalWithinCapacityStillConverts guards the other
// side: a dictionary that fits is untouched by the new check.
func TestConvertJob_CategoricalWithinCapacityStillConverts(t *testing.T) {
	source := newMockReader([]string{"answer"}, distinctRows(200))
	target := &collectWriter{}

	job := NewConvertJob(source, target)
	job.Schema = overflowSchema(t)

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 200 {
		t.Errorf("RowsConverted = %d, want 200", report.RowsConverted)
	}
	if got := report.Schema.Fields[0].Dictionary.Count(); got != 200 {
		t.Errorf("dictionary Count = %d, want 200", got)
	}
}
