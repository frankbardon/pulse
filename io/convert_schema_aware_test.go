package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// --- ConvertJob honours SchemaAwareReader -----------------------------------

// TestConvertJob_SchemaAwareReader_BypassesInference is the regression
// this file exists for. Registering `.sav` on formatFromExt makes
// `pulse convert survey.sav out.csv` reachable, and until ConvertJob
// consulted SchemaAwareReader that command re-inferred every column
// type from the text the reader rendered — silently throwing away the
// source dictionary that is the entire point of the SPSS adapter.
//
// The source deliberately does NOT implement ResetReader. Inference
// hard-requires one, so a convert that succeeds through this reader
// proves the inference pass was bypassed structurally rather than
// merely having its output overwritten.
func TestConvertJob_SchemaAwareReader_BypassesInference(t *testing.T) {
	src := &authoritativeReader{
		columns: []string{"score", "grade"},
		rows:    [][]string{{"1", "A"}, {"2", "B"}, {"3", "A"}},
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU16, CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
		}},
	}
	target := &collectWriter{}
	job := NewConvertJob(src, target)
	job.FS = afero.NewMemMapFs()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if src.calls != 1 {
		t.Errorf("PulseSchema called %d times, want 1", src.calls)
	}
	if report.RowsConverted != 3 {
		t.Errorf("RowsConverted = %d, want 3", report.RowsConverted)
	}
	// Inference over {"1","2","3"} would say u8, and over {"A","B","A"}
	// would say categorical_u8. The dictionary said otherwise.
	if got := report.Schema.Fields[0].Type; got != encoding.FieldTypeF64 {
		t.Errorf("score type = %s, want f64 (inference would have said u8)", got)
	}
	if got := report.Schema.Fields[1].Type; got != encoding.FieldTypeCategoricalU16 {
		t.Errorf("grade type = %s, want categorical_u16", got)
	}
}

// TestConvertJob_SchemaAwareReader_KeepPulseAtCarriesAuthoritativeTypes
// pins the half that actually persists. The exported CSV is text either
// way, so a convert could look correct on its output and still write a
// re-guessed cohort at KeepPulseAt — the file downstream analysis then
// runs against.
func TestConvertJob_SchemaAwareReader_KeepPulseAtCarriesAuthoritativeTypes(t *testing.T) {
	src := &resettableAuthoritativeReader{
		mockReader: newMockReader(
			[]string{"score", "grade"},
			[][]string{{"1", "A"}, {"2", "B"}},
		),
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU16, CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
		}},
	}
	fs := afero.NewMemMapFs()
	job := NewConvertJob(src, &collectWriter{})
	job.FS = fs
	job.KeepPulseAt = "kept.pulse"

	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	schema, _, _ := decodeAll(t, fs, "kept.pulse")
	if got := schema.Fields[0].Type; got != encoding.FieldTypeF64 {
		t.Errorf("kept.pulse score type = %s, want f64", got)
	}
	if got := schema.Fields[1].Type; got != encoding.FieldTypeCategoricalU16 {
		t.Errorf("kept.pulse grade type = %s, want categorical_u16", got)
	}
}

// TestConvertJob_SchemaAwareReader_ExplicitSchemaWins keeps the
// precedence identical to ImportJob's: the caller is the most specific
// instruction, so an explicit ConvertJob.Schema must not even consult
// the reader. Divergence between the two verbs here would be its own
// bug — the same file converted and imported would land differently
// typed.
func TestConvertJob_SchemaAwareReader_ExplicitSchemaWins(t *testing.T) {
	src := &authoritativeReader{
		columns: []string{"score"},
		rows:    [][]string{{"1"}, {"2"}},
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
		}},
	}
	job := NewConvertJob(src, &collectWriter{})
	job.FS = afero.NewMemMapFs()
	job.Schema = &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
	}}

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if src.calls != 0 {
		t.Errorf("PulseSchema called %d times; an explicit ConvertJob.Schema must not consult the reader", src.calls)
	}
	if got := report.Schema.Fields[0].Type; got != encoding.FieldTypeU8 {
		t.Errorf("score type = %s, want u8 (the explicit schema)", got)
	}
}

// TestConvertJob_SchemaAwareReader_DeclineFallsBackToInference pins the
// (nil, nil) opt-out. A reader may implement the interface and still say
// "not for this source"; that must be indistinguishable from not
// implementing it.
func TestConvertJob_SchemaAwareReader_DeclineFallsBackToInference(t *testing.T) {
	src := &resettableAuthoritativeReader{
		mockReader: newMockReader([]string{"score"}, [][]string{{"1"}, {"2"}}),
		schema:     nil,
	}
	job := NewConvertJob(src, &collectWriter{})
	job.FS = afero.NewMemMapFs()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if src.calls != 1 {
		t.Errorf("PulseSchema called %d times, want 1", src.calls)
	}
	if report.Schema.Fields[0].Type == encoding.FieldTypeF64 {
		t.Errorf("score type = f64; a declined schema must fall through to inference, which sees small integers")
	}
}

// TestConvertJob_SchemaAwareReader_ErrorFailsConvert mirrors the import
// rule: a source that HAS a dictionary and could not read it must not
// quietly produce a differently-typed output.
func TestConvertJob_SchemaAwareReader_ErrorFailsConvert(t *testing.T) {
	src := &resettableAuthoritativeReader{
		mockReader: newMockReader([]string{"score"}, [][]string{{"1"}}),
		err:        perrors.NewCodedError(perrors.PULSE_SPSS_DICT_INVALID, "boom"),
	}
	job := NewConvertJob(src, &collectWriter{})
	job.FS = afero.NewMemMapFs()

	if _, err := job.Run(context.Background()); err == nil {
		t.Fatal("Run: nil error; a failed authoritative-schema read must fail the convert, not fall back to inference")
	}
}

// TestConvertJob_Predict_SchemaAwareReader asserts predict reports the
// schema Run would actually use. A predict that disagreed with its own
// run is worse than no predict at all.
func TestConvertJob_Predict_SchemaAwareReader(t *testing.T) {
	src := &authoritativeReader{
		columns: []string{"score"},
		rows:    [][]string{{"1"}, {"2"}, {"3"}},
		schema: &encoding.Schema{Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 0},
		}},
	}
	job := NewConvertJob(src, &collectWriter{})
	job.FS = afero.NewMemMapFs()

	report, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if got := report.Schema.Fields[0].Type; got != encoding.FieldTypeF64 {
		t.Errorf("predicted score type = %s, want f64", got)
	}
	if report.EstimatedRows != 3 {
		t.Errorf("EstimatedRows = %d, want 3", report.EstimatedRows)
	}
}

// TestConvertJob_NonSchemaAwareReader_Unchanged pins the degrade path.
// Every pre-existing adapter (csv, tsv, ndjson, jsonarray, parquet,
// arrow, excel) reaches convert through this branch, so the new lookup
// must be invisible to them.
func TestConvertJob_NonSchemaAwareReader_Unchanged(t *testing.T) {
	src := newMockReader([]string{"score", "grade"}, [][]string{{"1", "A"}, {"2", "B"}})
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
	if len(report.SourceWarnings) != 0 {
		t.Errorf("SourceWarnings = %v, want none for a reader that emits no warnings", report.SourceWarnings)
	}
	if got, want := len(target.header), 2; got != want {
		t.Errorf("header columns = %d, want %d", got, want)
	}
}

// --- SourceWarningEmitter routing -------------------------------------------

// warningReader implements Reader + SourceWarningEmitter, the shape the
// SPSS adapter presents to the shared jobs.
type warningReader struct {
	*mockReader
	warnings []*perrors.CodedError
	calls    int
}

func (r *warningReader) Warnings() []*perrors.CodedError {
	r.calls++
	return r.warnings
}

var _ SourceWarningEmitter = (*warningReader)(nil)

func newWarningReader(codes ...perrors.Code) *warningReader {
	warns := make([]*perrors.CodedError, 0, len(codes))
	for _, c := range codes {
		warns = append(warns, perrors.NewCodedError(c, string(c)+" fired"))
	}
	return &warningReader{
		mockReader: newMockReader([]string{"score"}, [][]string{{"1"}, {"2"}}),
		warnings:   warns,
	}
}

// TestImportJob_SourceWarnings_Lifted is the routing this story wires.
// The SPSS reader raised these diagnostics from E2-S3 onward and nothing
// carried them out of the adapter, so a cohort could silently be built
// from a near-unique categorical or a demoted temporal column with the
// user never told.
func TestImportJob_SourceWarnings_Lifted(t *testing.T) {
	src := newWarningReader(
		perrors.PULSE_SPSS_CARDINALITY_HIGH,
		perrors.PULSE_SPSS_TEMPORAL_PRECISION,
	)
	job := NewImportJob(src, "out.pulse")
	job.FS = afero.NewMemMapFs()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.SourceWarnings) != 2 {
		t.Fatalf("SourceWarnings = %d, want 2", len(report.SourceWarnings))
	}
	if report.SourceWarnings[0].Code != perrors.PULSE_SPSS_CARDINALITY_HIGH {
		t.Errorf("SourceWarnings[0].Code = %s, want PULSE_SPSS_CARDINALITY_HIGH", report.SourceWarnings[0].Code)
	}
	// Collected once, after the row pass — a second collection would
	// double a set the adapter memoises.
	if src.calls != 1 {
		t.Errorf("Warnings() called %d times, want 1", src.calls)
	}
}

// TestImportJob_Predict_SourceWarnings_Lifted covers the no-execute
// verb. A predict against an authoritative source runs no inference at
// all, so PredictReport.Warnings is empty and SourceWarnings is the ONLY
// signal a user gets before committing to an import.
func TestImportJob_Predict_SourceWarnings_Lifted(t *testing.T) {
	src := newWarningReader(perrors.PULSE_SPSS_EXTENSION_UNKNOWN)
	job := NewImportJob(src, "out.pulse")
	job.FS = afero.NewMemMapFs()

	report, err := job.Predict(context.Background())
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if len(report.SourceWarnings) != 1 {
		t.Fatalf("SourceWarnings = %d, want 1", len(report.SourceWarnings))
	}
	if report.SourceWarnings[0].Code != perrors.PULSE_SPSS_EXTENSION_UNKNOWN {
		t.Errorf("code = %s, want PULSE_SPSS_EXTENSION_UNKNOWN", report.SourceWarnings[0].Code)
	}
}

// TestConvertJob_SourceWarnings_Lifted covers the third verb, and pins
// the distinction from OverlayWarnings: those come from the TARGET
// writer, these from the SOURCE reader, and a report must not conflate
// them.
func TestConvertJob_SourceWarnings_Lifted(t *testing.T) {
	src := newWarningReader(perrors.PULSE_SPSS_DATA_CASE_COUNT_MISMATCH)
	job := NewConvertJob(src, &collectWriter{})
	job.FS = afero.NewMemMapFs()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.SourceWarnings) != 1 {
		t.Fatalf("SourceWarnings = %d, want 1", len(report.SourceWarnings))
	}
	if len(report.OverlayWarnings) != 0 {
		t.Errorf("OverlayWarnings = %v; source diagnostics must not land on the writer's slot", report.OverlayWarnings)
	}
}

// TestSourceWarnings_AbsentInterfaceIsNil pins the degrade path: a
// reader that does not implement the interface contributes nil, not an
// empty non-nil slice, so `omitempty`-style consumers and equality
// checks against pre-interface reports still hold.
func TestSourceWarnings_AbsentInterfaceIsNil(t *testing.T) {
	job := NewImportJob(newMockReader([]string{"a"}, [][]string{{"1"}}), "out.pulse")
	job.FS = afero.NewMemMapFs()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.SourceWarnings != nil {
		t.Errorf("SourceWarnings = %v, want nil for a non-emitting reader", report.SourceWarnings)
	}
}

// TestSourceWarnings_EmptySliceIsNil keeps the same guarantee for a
// reader that implements the interface and simply had nothing to say —
// the overwhelmingly common case for a clean `.sav`.
func TestSourceWarnings_EmptySliceIsNil(t *testing.T) {
	src := newWarningReader()
	job := NewImportJob(src, "out.pulse")
	job.FS = afero.NewMemMapFs()

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.SourceWarnings != nil {
		t.Errorf("SourceWarnings = %v, want nil when the emitter yields nothing", report.SourceWarnings)
	}
}
