package spss

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/spf13/afero"
)

func inexactOf(warns []*perr.CodedError) []*perr.CodedError {
	var out []*perr.CodedError
	for _, w := range warns {
		if w.Code == perr.PULSE_SPSS_INTEGER_INEXACT {
			out = append(out, w)
		}
	}
	return out
}

func u64Cohort(t *testing.T, values ...any) (afero.Fs, string) {
	t.Helper()
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{float64(i), v}
	}
	return vecCohort(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeF64, Description: "Row id"},
		{Name: "key", Type: encoding.FieldTypeU64, Nullable: true, Description: "Account key"},
	}}, rows)
}

// TestIntegerInexact_WarnsPastTwoToThe53: a u64 past 2^53 has no unambiguous
// double, so the export says so — one PULSE_SPSS_INTEGER_INEXACT per
// variable, naming the variable, the count and the first row. 2^53 itself is
// exact and never warns. Before this the value was rounded silently.
func TestIntegerInexact_WarnsPastTwoToThe53(t *testing.T) {
	t.Parallel()

	t.Run("at 2^53", func(t *testing.T) {
		t.Parallel()
		fs, cohort := u64Cohort(t, uint64(7), uint64(1<<53), nil)
		_, rep, err := exportVecSav(t, fs, cohort, WriterOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if w := inexactOf(rep.TargetWarnings); len(w) != 0 {
			t.Errorf("warnings = %v, want none at 2^53", w)
		}
	})

	t.Run("past 2^53", func(t *testing.T) {
		t.Parallel()
		fs, cohort := u64Cohort(t, uint64(7), uint64(1<<53), uint64(1<<53+1), nil, uint64(1<<63+5))
		_, rep, err := exportVecSav(t, fs, cohort, WriterOptions{})
		if err != nil {
			t.Fatal(err)
		}
		w := inexactOf(rep.TargetWarnings)
		if len(w) != 1 {
			t.Fatalf("warnings = %v, want one PULSE_SPSS_INTEGER_INEXACT", rep.TargetWarnings)
		}
		d := w[0].Details
		if d[perr.DetailSPSSVariable] != "key" || d[perr.DetailSPSSField] != "key" ||
			d[perr.DetailSPSSCount] != int64(2) || d[perr.DetailSPSSFirstRow] != int64(3) ||
			d[perr.DetailSPSSValue] != "9007199254740993" {
			t.Errorf("details = %v, want key / count 2 / first_row 3 / value 2^53+1", d)
		}
	})
}

// TestIntegerInexact_StrictRefusesAndWritesNothing: under ExportJob.Strict the
// same value is the export's error, the writer holds no bytes and nothing
// lands on the filesystem.
func TestIntegerInexact_StrictRefusesAndWritesNothing(t *testing.T) {
	t.Parallel()

	fs, cohort := u64Cohort(t, uint64(1), uint64(1<<53+1))
	w := NewWriter(fs, "out.sav", WriterOptions{})
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	job.Strict = true
	_, err := job.Run(context.Background())
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_SPSS_INTEGER_INEXACT {
		t.Fatalf("err = %v, want PULSE_SPSS_INTEGER_INEXACT", err)
	}
	if ce.Details[perr.DetailSPSSFirstRow] != int64(2) || ce.Details[perr.DetailSPSSVariable] != "key" {
		t.Errorf("details = %v", ce.Details)
	}
	if len(w.Bytes()) != 0 {
		t.Errorf("the writer holds %d bytes after a refused export", len(w.Bytes()))
	}
	if ok, _ := afero.Exists(fs, "out.sav"); ok {
		t.Error("out.sav exists after a refused export")
	}

	// Strict with nothing past 2^53 exports as usual.
	fs2, cohort2 := u64Cohort(t, uint64(1), uint64(1<<53))
	w2 := NewWriter(fs2, "out.sav", WriterOptions{})
	job2 := pio.NewExportJob(cohort2, w2)
	job2.FS = fs2
	job2.Strict = true
	if _, err := job2.Run(context.Background()); err != nil {
		t.Fatalf("Strict export at 2^53: %v", err)
	}
}

// TestIntegerInexact_PredictListsTheRisk: export predict cannot read a record,
// so it lists PULSE_SPSS_INTEGER_INEXACT — without count or row — for every
// u64 variable, the same code and variable the run names when a value is
// past 2^53. A cohort with no u64 lists nothing.
func TestIntegerInexact_PredictListsTheRisk(t *testing.T) {
	t.Parallel()

	predict := func(fs afero.Fs, cohort string) []*perr.CodedError {
		t.Helper()
		job := pio.NewExportJob(cohort, NewWriterToBuffer(WriterOptions{}))
		job.FS = fs
		rep, err := job.Predict(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return inexactOf(rep.TargetWarnings)
	}

	fs, cohort := u64Cohort(t, uint64(1), uint64(1<<53+1))
	risk := predict(fs, cohort)
	if len(risk) != 1 || risk[0].Details[perr.DetailSPSSVariable] != "key" {
		t.Fatalf("predict = %v, want one risk for key", risk)
	}
	if _, ok := risk[0].Details[perr.DetailSPSSCount]; ok {
		t.Errorf("a predicted risk carries a count: %v", risk[0].Details)
	}
	_, rep, err := exportVecSav(t, fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run := inexactOf(rep.TargetWarnings)
	if len(run) != 1 || run[0].Code != risk[0].Code || run[0].Details[perr.DetailSPSSVariable] != risk[0].Details[perr.DetailSPSSVariable] {
		t.Errorf("run = %v, want the predicted code and variable", run)
	}

	fs2, cohort2 := vecCohort(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU32, Description: "Count"},
	}}, [][]any{{uint64(1)}})
	if r := predict(fs2, cohort2); len(r) != 0 {
		t.Errorf("predict on a u64-free cohort = %v, want none", r)
	}
}

// TestIntegerInexact_ConvertStrictRowsRefuses: a convert into `.sav` encodes
// at Close; ConvertJob.StrictRows reaches the writer as ConvertSource.Strict,
// so Close fails and writes nothing. Without it Close warns.
func TestIntegerInexact_ConvertStrictRowsRefuses(t *testing.T) {
	t.Parallel()

	// The projected case is an UNFAITHFUL row stream: no schema or sidecar
	// may be handed over, and the strict half still must be.
	for _, tc := range []struct {
		strict   bool
		includes []string
	}{{false, nil}, {true, nil}, {true, []string{"key"}}} {
		strict := tc.strict
		fs := afero.NewMemMapFs()
		src := &plainRowsReader{
			columns: []string{"key", "note"},
			rows:    [][]string{{"1", "a"}, {"9007199254740993", "b"}},
		}
		w := NewWriter(fs, "out.sav", WriterOptions{})
		job := pio.NewConvertJob(src, w)
		job.FS = fs
		job.StrictRows = strict
		job.Includes = tc.includes
		if _, err := job.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		err := w.Close()
		exists, _ := afero.Exists(fs, "out.sav")
		if strict {
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_SPSS_INTEGER_INEXACT {
				t.Errorf("strict Close = %v, want PULSE_SPSS_INTEGER_INEXACT", err)
			}
			if exists {
				t.Error("out.sav exists after a refused convert")
			}
			continue
		}
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
		if w := inexactOf(w.Warnings()); len(w) != 1 || !exists {
			t.Errorf("warnings = %v (file %v), want one PULSE_SPSS_INTEGER_INEXACT and a file", w, exists)
		}
	}
}
