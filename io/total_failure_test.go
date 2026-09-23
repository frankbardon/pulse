package io

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// failingWriter is a Writer whose WriteRow refuses. failOn selects the
// 1-based row indices that fail; a nil failOn fails every row, which is
// the shape the Arrow / Parquet set-column bug produced for real — every
// WriteRow rejected and the job still reported success.
type failingWriter struct {
	failOn  map[int]bool
	row     int
	written int
	closed  bool
}

func (w *failingWriter) WriteHeader(columns []string) error { return nil }

func (w *failingWriter) WriteRow(values []any) error {
	w.row++
	if w.failOn == nil || w.failOn[w.row] {
		return fmt.Errorf("writer refused row %d", w.row)
	}
	w.written++
	return nil
}

func (w *failingWriter) Close() error { w.closed = true; return nil }

// setCellWithTokens builds a set cell carrying n distinct tokens joined
// by the default delimiter.
func setCellWithTokens(n int) string {
	toks := make([]string, n)
	for i := range toks {
		toks[i] = fmt.Sprintf("t%04d", i)
	}
	return strings.Join(toks, DefaultSetDelimiter)
}

// importedCohort writes a cohort with the given schema and rows and
// returns the filesystem it lives on. Every row must be importable.
func importedCohort(t *testing.T, schema *encoding.Schema, header []string, rows [][]string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := &ImportJob{
		Source: &stringRowsReader{header: header, rows: rows},
		Target: "c.pulse",
		Schema: schema,
		FS:     fs,
	}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("seeding cohort: %v", err)
	}
	if rep.RowsImported != len(rows) {
		t.Fatalf("seeding cohort: RowsImported = %d, want %d (%v)", rep.RowsImported, len(rows), rep.RowErrors)
	}
	return fs
}

func codedFrom(t *testing.T, err error) *perrors.CodedError {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("error %v is not a *errors.CodedError; `pulse errors lookup` cannot resolve it", err)
	}
	return ce
}

// TestImportJob_EveryRowFailed_ReturnsCodedError is the regression for the
// shape that hid three bugs in this effort: RowsImported == 0 with N row
// errors and a nil error return. Zero rows out of a non-empty source is
// total failure, not partial success, and Run must say so in the error
// return every caller already checks.
func TestImportJob_EveryRowFailed_ReturnsCodedError(t *testing.T) {
	// A set_u256 column whose cells carry more distinct tokens than the
	// rung can hold: the row pass cannot convert the column on ANY row,
	// which is exactly the wide-set shape that imported 0 of 480 rows
	// while reporting success.
	wideSetCell := setCellWithTokens(int(encoding.FieldTypeSetU256.MaxSetEntries()) + 1)

	cases := []struct {
		name   string
		schema *encoding.Schema
		header []string
		rows   [][]string
	}{
		{
			name: "wide set column the row pass cannot convert",
			schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "picks", Type: encoding.FieldTypeSetU256, CsvColumnIdx: 0, Dictionary: encoding.NewDictionary()},
			}},
			header: []string{"picks"},
			rows:   [][]string{{wideSetCell}, {wideSetCell}, {wideSetCell}},
		},
		{
			name: "narrow column no cell parses as",
			schema: &encoding.Schema{Fields: []encoding.Field{
				{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
			}},
			header: []string{"n"},
			rows:   [][]string{{"abc"}, {"def"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			job := &ImportJob{
				Source: &stringRowsReader{header: tc.header, rows: tc.rows},
				Target: "out.pulse",
				Schema: tc.schema,
				FS:     fs,
			}
			rep, err := job.Run(context.Background())
			if err == nil {
				t.Fatalf("Run returned nil with RowsImported = %d over %d source rows; total failure must not look like success",
					rep.RowsImported, len(tc.rows))
			}
			ce := codedFrom(t, err)
			if ce.Code != perrors.PULSE_IMPORT_ROW_ERROR {
				t.Errorf("code = %s, want %s", ce.Code, perrors.PULSE_IMPORT_ROW_ERROR)
			}
			if got := ce.Details["rows_failed"]; got != len(tc.rows) {
				t.Errorf("details[rows_failed] = %v, want %d", got, len(tc.rows))
			}
			if got := ce.Details["rows_read"]; got != len(tc.rows) {
				t.Errorf("details[rows_read] = %v, want %d", got, len(tc.rows))
			}
			if got := ce.Details["first_row"]; got != 1 {
				t.Errorf("details[first_row] = %v, want 1", got)
			}
			if ce.Message == "" {
				t.Error("message is empty")
			}
			// Nothing was importable, so no cohort should be left behind:
			// a zero-row .pulse alongside a hard error is a trap for the
			// next command that opens it.
			if ok, _ := afero.Exists(fs, "out.pulse"); ok {
				t.Error("out.pulse was written despite total import failure")
			}
		})
	}
}

// TestImportJob_EmptySource_StaysLegitimate pins the boundary the
// total-failure check must not cross. Zero data rows and zero row errors
// is an empty cohort, which is a legitimate outcome.
//
// The second arm is a reader that declares a header and then yields no
// rows at all. That used to be reachable by accident — ndjson.Reader
// ate its first object, so a one-record file presented zero data rows —
// and the accident is gone, but the shape is still legitimate for any
// source whose header is out-of-band (a CSV with only a header line).
func TestImportJob_EmptySource_StaysLegitimate(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows [][]string
	}{
		{"no data rows at all", nil},
		{"header declared, no rows yielded", [][]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			job := &ImportJob{
				Source: &stringRowsReader{header: []string{"n"}, rows: tc.rows},
				Target: "empty.pulse",
				Schema: &encoding.Schema{Fields: []encoding.Field{
					{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
				}},
				FS: fs,
			}
			rep, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v — an empty source is an empty cohort, not a failure", err)
			}
			if rep.RowsImported != 0 {
				t.Errorf("RowsImported = %d, want 0", rep.RowsImported)
			}
			if len(rep.RowErrors) != 0 {
				t.Errorf("RowErrors = %v, want none", rep.RowErrors)
			}
			if ok, _ := afero.Exists(fs, "empty.pulse"); !ok {
				t.Error("empty.pulse was not written; an empty cohort is still a cohort")
			}
		})
	}
}

// TestImportJob_PartialFailure_Unchanged pins the other boundary: some
// rows in, some errored, is still report-plus-nil. This story changes only
// the total-failure case.
func TestImportJob_PartialFailure_Unchanged(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := &ImportJob{
		Source: &stringRowsReader{header: []string{"n"}, rows: [][]string{{"1"}, {"abc"}, {"3"}}},
		Target: "partial.pulse",
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		}},
		FS: fs,
	}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v — partial failure must keep returning nil", err)
	}
	if rep.RowsImported != 2 {
		t.Errorf("RowsImported = %d, want 2", rep.RowsImported)
	}
	if len(rep.RowErrors) != 1 {
		t.Errorf("RowErrors = %d, want 1", len(rep.RowErrors))
	}
}

// TestExportJob_EveryRowFailed_ReturnsCodedError is the export arm. Arrow
// export of any set column produced RowsExported == 0 with 480 RowErrors
// and a nil error; `pulse export parquet` wrote a zero-row file and
// reported success.
func TestExportJob_EveryRowFailed_ReturnsCodedError(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
	}}
	fs := importedCohort(t, schema, []string{"n"}, [][]string{{"1"}, {"2"}, {"3"}})

	w := &failingWriter{}
	job := &ExportJob{Source: "c.pulse", Target: w, FS: fs}
	rep, err := job.Run(context.Background())
	if err == nil {
		t.Fatalf("Run returned nil with RowsExported = %d and %d row errors", rep.RowsExported, len(rep.RowErrors))
	}
	ce := codedFrom(t, err)
	if ce.Code != perrors.PULSE_EXPORT_ROW_ERROR {
		t.Errorf("code = %s, want %s", ce.Code, perrors.PULSE_EXPORT_ROW_ERROR)
	}
	if got := ce.Details["rows_failed"]; got != 3 {
		t.Errorf("details[rows_failed] = %v, want 3", got)
	}
	if got := ce.Details["rows_read"]; got != 3 {
		t.Errorf("details[rows_read] = %v, want 3", got)
	}
	if got := ce.Details["first_row"]; got != 1 {
		t.Errorf("details[first_row] = %v, want 1", got)
	}
}

// codedFailingWriter refuses every row with a CodedError of its own,
// standing in for a target that knows exactly why it cannot represent a
// column (the `.sav` writer's refusals are this shape).
type codedFailingWriter struct{ code perrors.Code }

func (w *codedFailingWriter) WriteHeader(columns []string) error { return nil }
func (w *codedFailingWriter) WriteRow(values []any) error {
	return perrors.NewCodedError(w.code, "this target cannot represent the column")
}
func (w *codedFailingWriter) Close() error { return nil }

// TestExportJob_EveryRowFailed_PrefersTheRowErrorsOwnCode pins the code
// selection. The verdict is only useful with `pulse errors lookup` if it
// resolves to WHY the rows failed; falling back to the generic
// PULSE_EXPORT_ROW_ERROR when the row error already carried a specific
// code would bury the diagnosis one level down in the message text.
func TestExportJob_EveryRowFailed_PrefersTheRowErrorsOwnCode(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
	}}
	fs := importedCohort(t, schema, []string{"n"}, [][]string{{"1"}, {"2"}})

	job := &ExportJob{
		Source: "c.pulse",
		Target: &codedFailingWriter{code: perrors.PULSE_SPSS_EXPORT_UNSUPPORTED},
		FS:     fs,
	}
	_, err := job.Run(context.Background())
	if err == nil {
		t.Fatal("Run returned nil; every row was refused")
	}
	ce := codedFrom(t, err)
	if ce.Code != perrors.PULSE_SPSS_EXPORT_UNSUPPORTED {
		t.Errorf("code = %s, want %s — the first row error's own code must win over the fallback",
			ce.Code, perrors.PULSE_SPSS_EXPORT_UNSUPPORTED)
	}
}

// TestExportJob_EmptyCohort_StaysLegitimate mirrors the import boundary:
// a cohort with no records exports zero rows and that is not a failure,
// even through a writer that would refuse every row.
func TestExportJob_EmptyCohort_StaysLegitimate(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
	}}
	fs := importedCohort(t, schema, []string{"n"}, nil)

	job := &ExportJob{Source: "c.pulse", Target: &failingWriter{}, FS: fs}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v — an empty cohort exports zero rows legitimately", err)
	}
	if rep.RowsExported != 0 {
		t.Errorf("RowsExported = %d, want 0", rep.RowsExported)
	}
	if len(rep.RowErrors) != 0 {
		t.Errorf("RowErrors = %v, want none", rep.RowErrors)
	}
}

// TestExportJob_PartialFailure_Unchanged pins the export partial arm.
func TestExportJob_PartialFailure_Unchanged(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
	}}
	fs := importedCohort(t, schema, []string{"n"}, [][]string{{"1"}, {"2"}, {"3"}})

	job := &ExportJob{Source: "c.pulse", Target: &failingWriter{failOn: map[int]bool{2: true}}, FS: fs}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v — partial failure must keep returning nil", err)
	}
	if rep.RowsExported != 2 {
		t.Errorf("RowsExported = %d, want 2", rep.RowsExported)
	}
	if len(rep.RowErrors) != 1 {
		t.Errorf("RowErrors = %d, want 1", len(rep.RowErrors))
	}
}

// TestConvertJob_EveryRowFailed_ReturnsCodedError is the convert arm.
//
// `pulse convert` is source-format → target-format (a `.pulse` source is
// reserved and excluded from SupportedImport), so every RowError it can
// record comes from the TARGET writer: the row loop hands raw cell text
// through and only WriteRow can refuse. The verdict therefore falls back
// to PULSE_EXPORT_ROW_ERROR, by provenance — PULSE_IMPORT_ROW_ERROR would
// point the caller at a source that never objected.
func TestConvertJob_EveryRowFailed_ReturnsCodedError(t *testing.T) {
	job := &ConvertJob{
		Source: &stringRowsReader{header: []string{"n"}, rows: [][]string{{"1"}, {"2"}, {"3"}}},
		Target: &failingWriter{},
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		}},
		FS: afero.NewMemMapFs(),
	}
	rep, err := job.Run(context.Background())
	if err == nil {
		t.Fatalf("Run returned nil with RowsConverted = %d and %d row errors", rep.RowsConverted, len(rep.RowErrors))
	}
	ce := codedFrom(t, err)
	if ce.Code != perrors.PULSE_EXPORT_ROW_ERROR {
		t.Errorf("code = %s, want %s", ce.Code, perrors.PULSE_EXPORT_ROW_ERROR)
	}
	if got := ce.Details["rows_failed"]; got != 3 {
		t.Errorf("details[rows_failed] = %v, want 3", got)
	}
	if got := ce.Details["rows_read"]; got != 3 {
		t.Errorf("details[rows_read] = %v, want 3", got)
	}
	if got := ce.Details["first_row"]; got != 1 {
		t.Errorf("details[first_row] = %v, want 1", got)
	}
}

// TestConvertJob_EveryRowFailed_PrefersTheRowErrorsOwnCode mirrors the
// export arm: a target that knows why it refused keeps its own code.
func TestConvertJob_EveryRowFailed_PrefersTheRowErrorsOwnCode(t *testing.T) {
	job := &ConvertJob{
		Source: &stringRowsReader{header: []string{"n"}, rows: [][]string{{"1"}, {"2"}}},
		Target: &codedFailingWriter{code: perrors.PULSE_SPSS_EXPORT_UNSUPPORTED},
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		}},
		FS: afero.NewMemMapFs(),
	}
	if _, err := job.Run(context.Background()); err == nil {
		t.Fatal("Run returned nil; every row was refused")
	} else if ce := codedFrom(t, err); ce.Code != perrors.PULSE_SPSS_EXPORT_UNSUPPORTED {
		t.Errorf("code = %s, want %s", ce.Code, perrors.PULSE_SPSS_EXPORT_UNSUPPORTED)
	}
}

// TestConvertJob_EveryRowFailed_WritesNoIntermediatePulse pins the
// placement of the verdict. KeepPulseAt's re-import reads the SOURCE, not
// the converted rows, so a target-side total failure would otherwise
// leave a perfectly good cohort on disk under a command that errored —
// the same trap the import arm avoids by refusing before its write.
func TestConvertJob_EveryRowFailed_WritesNoIntermediatePulse(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := &ConvertJob{
		Source:      &stringRowsReader{header: []string{"n"}, rows: [][]string{{"1"}, {"2"}}},
		Target:      &failingWriter{},
		KeepPulseAt: "mid.pulse",
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		}},
		FS: fs,
	}
	if _, err := job.Run(context.Background()); err == nil {
		t.Fatal("Run returned nil; every row was refused")
	}
	if ok, _ := afero.Exists(fs, "mid.pulse"); ok {
		t.Error("mid.pulse was written despite total convert failure")
	}
}

// TestConvertJob_PartialFailure_Unchanged pins the convert partial arm.
func TestConvertJob_PartialFailure_Unchanged(t *testing.T) {
	job := &ConvertJob{
		Source: &stringRowsReader{header: []string{"n"}, rows: [][]string{{"1"}, {"2"}, {"3"}}},
		Target: &failingWriter{failOn: map[int]bool{2: true}},
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		}},
		FS: afero.NewMemMapFs(),
	}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v — partial failure must keep returning nil", err)
	}
	if rep.RowsConverted != 2 {
		t.Errorf("RowsConverted = %d, want 2", rep.RowsConverted)
	}
	if len(rep.RowErrors) != 1 {
		t.Errorf("RowErrors = %d, want 1", len(rep.RowErrors))
	}
}

// TestConvertJob_EmptySource_StaysLegitimate pins the convert boundary: a
// source with no data rows converts to an empty target, through a writer
// that would have refused every row.
func TestConvertJob_EmptySource_StaysLegitimate(t *testing.T) {
	job := &ConvertJob{
		Source: &stringRowsReader{header: []string{"n"}},
		Target: &failingWriter{},
		Schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		}},
		FS: afero.NewMemMapFs(),
	}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v — an empty source converts to an empty target, not a failure", err)
	}
	if rep.RowsConverted != 0 || len(rep.RowErrors) != 0 {
		t.Fatalf("RowsConverted = %d, RowErrors = %v; want 0 and none", rep.RowsConverted, rep.RowErrors)
	}
}
