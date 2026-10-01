package io

// The ConvertSource carry: what a convert tells a target about its SOURCE.
//
// These are the gate's own tests — what is carried, what is not, and what a
// recipient is entitled to assume about the binding between a schema field
// and a cell of the row it is handed. The fidelity consequences are tested
// where they are visible, in io/spss (convert_sav_test.go); here the claim
// is only about the channel, so the recipient is a stub that records.

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// --- the recipient ---------------------------------------------------------

// sourceAwareCollector is a collectWriter that also implements
// SourceAwareWriter, recording what it was offered and when.
type sourceAwareCollector struct {
	collectWriter
	calls int
	got   ConvertSource
	// headerAt is the call count at the moment WriteHeader ran, so the
	// "before WriteHeader" ordering claim is an observation rather than an
	// assumption.
	headerAt int
}

func (w *sourceAwareCollector) SetConvertSource(src ConvertSource) {
	w.calls++
	w.got = src
}

func (w *sourceAwareCollector) WriteHeader(columns []string) error {
	w.headerAt = w.calls
	return w.collectWriter.WriteHeader(columns)
}

var _ SourceAwareWriter = (*sourceAwareCollector)(nil)

// sidecarReader is an authoritativeReader that also emits a sidecar, which
// is the shape of io/spss's `.sav` reader: declared schema plus
// format-native metadata the `.pulse` format has nowhere to hold.
type sidecarReader struct {
	*resettableAuthoritativeReader
	wrote string
}

func (r *sidecarReader) WriteSidecar(_ afero.Fs, cohortPath string) error {
	r.wrote = cohortPath
	return nil
}

var _ SidecarEmitter = (*sidecarReader)(nil)

func newSidecarReader(columns []string, rows [][]string, schema *encoding.Schema) *sidecarReader {
	return &sidecarReader{resettableAuthoritativeReader: &resettableAuthoritativeReader{
		mockReader: newMockReader(columns, rows),
		schema:     schema,
	}}
}

func carrySchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		// CsvColumnIdx is deliberately NOT the field index: the source
		// renders "grade" first and "score" second, so a recipient that
		// took these indices verbatim would bind every cell to the wrong
		// column. See TestConvertSource_ColumnIndexIsReBasedOnTheEmittedRow.
		{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 1},
		{Name: "grade", Type: encoding.FieldTypeCategoricalU16, CsvColumnIdx: 0, Dictionary: encoding.NewDictionary()},
	}}
}

func runCarry(t *testing.T, mutate func(*ConvertJob)) (*sourceAwareCollector, *sidecarReader) {
	t.Helper()
	src := newSidecarReader(
		[]string{"grade", "score"},
		[][]string{{"A", "1"}, {"B", "2"}},
		carrySchema(),
	)
	target := &sourceAwareCollector{}
	job := NewConvertJob(src, target)
	job.FS = afero.NewMemMapFs()
	if mutate != nil {
		mutate(job)
	}
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return target, src
}

// --- what is carried -------------------------------------------------------

// TestConvertSource_DeclaredSchemaAndSidecarAreCarried is the headline: a
// source that declares a schema and emits a sidecar hands BOTH to a target
// that rebuilds a cohort, before the header is written.
func TestConvertSource_DeclaredSchemaAndSidecarAreCarried(t *testing.T) {
	target, src := runCarry(t, nil)

	if target.calls != 1 {
		t.Fatalf("SetConvertSource called %d time(s), want exactly 1", target.calls)
	}
	if target.headerAt != 1 {
		t.Errorf("WriteHeader ran after %d SetConvertSource call(s), want 1 — the facts must arrive first, "+
			"because a recipient may consult them from the first buffered row", target.headerAt)
	}
	if target.got.Schema == nil {
		t.Fatalf("Schema was not carried; the source declared one and it is the only record of the declared types")
	}
	if got := target.got.Schema.Fields[0].Type; got != encoding.FieldTypeF64 {
		t.Errorf("carried score type = %s, want f64 — inference over {\"1\",\"2\"} would say u8", got)
	}
	if target.got.Sidecar == nil {
		t.Fatalf("Sidecar was not carried; the source implements SidecarEmitter")
	}
	if target.got.Sidecar != SidecarEmitter(src) {
		t.Errorf("the carried Sidecar is not the source reader")
	}
}

// TestConvertSource_ColumnIndexIsReBasedOnTheEmittedRow pins the binding a
// recipient relies on.
//
// The row a target receives is built by convert's own loop, one cell per
// schema field in FIELD order — not in the source's column order. The
// fixture's schema declares the opposite order on purpose, so a carried
// CsvColumnIdx passed through verbatim would bind "score" to the grade cell
// and vice versa: two well-formed columns holding each other's values, with
// nothing downstream able to notice.
func TestConvertSource_ColumnIndexIsReBasedOnTheEmittedRow(t *testing.T) {
	target, _ := runCarry(t, nil)

	if target.got.Schema == nil {
		t.Fatalf("Schema was not carried")
	}
	for i, f := range target.got.Schema.Fields {
		if f.CsvColumnIdx != i {
			t.Errorf("carried field %q has CsvColumnIdx %d, want %d (its position in the emitted row)",
				f.Name, f.CsvColumnIdx, i)
		}
	}
	// And the source's own schema is untouched: it is reported on
	// ConvertReport.Schema and drives the KeepPulseAt import, both of which
	// read cells at the SOURCE index.
	if got := carrySchema().Fields[0].CsvColumnIdx; got != 1 {
		t.Fatalf("fixture drift: the declared score index is %d, want 1", got)
	}
}

// TestConvertSource_InferredSchemaIsNotCarried is the "be precise about
// which path is which" half. A schema convert INFERRED is not a declaration,
// so there is nothing to preserve and the recipient re-infers — the same
// guess over the same values. The sidecar arm is independent and still
// carried when the source emits one.
func TestConvertSource_InferredSchemaIsNotCarried(t *testing.T) {
	src := newMockReader([]string{"score"}, [][]string{{"1"}, {"2"}})
	target := &sourceAwareCollector{}
	job := NewConvertJob(src, target)
	job.FS = afero.NewMemMapFs()
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if target.calls != 0 {
		t.Fatalf("SetConvertSource called %d time(s) for a source that declares nothing and emits no sidecar, want 0",
			target.calls)
	}
}

// TestConvertSource_ExplicitJobSchemaIsCarried: the caller's own
// ConvertJob.Schema is a declaration too — the most specific one there is.
func TestConvertSource_ExplicitJobSchemaIsCarried(t *testing.T) {
	src := newMockReader([]string{"score"}, [][]string{{"1"}, {"2"}})
	target := &sourceAwareCollector{}
	job := NewConvertJob(src, target)
	job.FS = afero.NewMemMapFs()
	job.Schema = &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
	}}

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if target.calls != 1 {
		t.Fatalf("SetConvertSource called %d time(s), want 1", target.calls)
	}
	if target.got.Schema == nil || target.got.Schema.Fields[0].Type != encoding.FieldTypeF64 {
		t.Errorf("the explicit ConvertJob.Schema was not carried: %+v", target.got.Schema)
	}
}

// --- what is not carried ---------------------------------------------------

// TestConvertSource_ProjectionCarriesNothing: --include drops columns from
// the emitted row, so field i is no longer cell i and the sidecar describes
// variables the rebuilt cohort does not have. Carrying facts that do not
// describe the stream is the silent wrong answer; carrying none leaves the
// target on its unchanged inference path.
func TestConvertSource_ProjectionCarriesNothing(t *testing.T) {
	target, _ := runCarry(t, func(j *ConvertJob) { j.Includes = []string{"grade"} })

	if target.calls != 0 {
		t.Fatalf("SetConvertSource called %d time(s) under a projection, want 0; the carried schema would "+
			"describe %d field(s) against a %d-cell row", target.calls, 2, len(target.rows[0]))
	}
}

// TestConvertSource_LabelAugmentCarriesNothing: an augmenting binding
// INSERTS a "<field>_label" cell, so every field after it shifts.
func TestConvertSource_LabelAugmentCarriesNothing(t *testing.T) {
	target, _ := runCarry(t, func(j *ConvertJob) {
		j.LabelResolver = newFakeResolver("grade", types.LabelModeAugment, map[string]string{"A": "Alpha"})
	})

	if target.calls != 0 {
		t.Fatalf("SetConvertSource called %d time(s) under an augmenting label binding, want 0", target.calls)
	}
}

// TestConvertSource_LabelReplaceCarriesNothing: a replacing binding keeps
// the row's shape and rewrites its CELLS, so a categorical's carried
// dictionary no longer describes what a rebuilt cohort would read.
func TestConvertSource_LabelReplaceCarriesNothing(t *testing.T) {
	target, _ := runCarry(t, func(j *ConvertJob) {
		j.LabelResolver = newFakeResolver("grade", types.LabelModeReplace, map[string]string{"A": "Alpha"})
	})

	if target.calls != 0 {
		t.Fatalf("SetConvertSource called %d time(s) under a replacing label binding, want 0", target.calls)
	}
}

// TestConvertSource_PlainWriterIsUntouched is the no-behaviour-change
// claim for every other adapter: a target that does not implement
// SourceAwareWriter is not consulted, and its output is what it always was.
func TestConvertSource_PlainWriterIsUntouched(t *testing.T) {
	src := newSidecarReader(
		[]string{"grade", "score"},
		[][]string{{"A", "1"}, {"B", "2"}},
		carrySchema(),
	)
	target := &collectWriter{}
	job := NewConvertJob(src, target)
	job.FS = afero.NewMemMapFs()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsConverted != 2 {
		t.Errorf("RowsConverted = %d, want 2", report.RowsConverted)
	}
	if len(target.header) != 2 || target.header[0] != "score" || target.header[1] != "grade" {
		t.Errorf("header = %v, want [score grade]", target.header)
	}
	if src.wrote != "" {
		t.Errorf("the source was asked to write a sidecar at %q; a plain writer rebuilds no cohort "+
			"and must not trigger one", src.wrote)
	}
}
