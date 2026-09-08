package synth_test

import (
	"errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
)

// fidelitySchema builds a minimal merged schema shape: one numeric
// field, one categorical field, and the appended _synthetic
// packed_bool tag — exactly what buildMergedSchema produces.
func fidelitySchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "country", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: synth.SyntheticFieldName, Type: encoding.FieldTypePackedBool},
	}}
}

// TestBuildFidelityReport_ClassifiesFieldsAndSkipsSynthetic locks in
// which fields get a TEST_KS vs TEST_CHISQ entry, that the _synthetic
// column itself is never entered, and that a field of neither numeric
// nor categorical type (packed_bool "flag") is skipped rather than
// guessed at.
func TestBuildFidelityReport_ClassifiesFieldsAndSkipsSynthetic(t *testing.T) {
	var seen []*types.Test
	run := func(physical, presented *encoding.Schema, records []byte, test *types.Test) (*types.TestResult, error) {
		seen = append(seen, test)
		return &types.TestResult{Type: test.Type, Statistic: 1, PValue: 1}, nil
	}

	report := synth.BuildFidelityReport(fidelitySchema(), nil, 10, 5, run)

	if len(report.Fields) != 2 {
		t.Fatalf("len(Fields) = %d, want 2 (score, country only)", len(report.Fields))
	}
	if report.Fields[0].Field != "score" || report.Fields[0].Test != types.TEST_KS {
		t.Errorf("Fields[0] = %+v, want score/TEST_KS", report.Fields[0])
	}
	if report.Fields[1].Field != "country" || report.Fields[1].Test != types.TEST_CHISQ {
		t.Errorf("Fields[1] = %+v, want country/TEST_CHISQ", report.Fields[1])
	}
	if report.SourceRows != 10 || report.SyntheticRows != 5 {
		t.Errorf("SourceRows/SyntheticRows = %d/%d, want 10/5", report.SourceRows, report.SyntheticRows)
	}

	if len(seen) != 2 {
		t.Fatalf("run called %d times, want 2", len(seen))
	}
	if seen[0].Field != "score" || seen[0].SplitBy != synth.SyntheticFieldName {
		t.Errorf("KS test spec = %+v, want Field=score SplitBy=%s", seen[0], synth.SyntheticFieldName)
	}
	if seen[1].Rows != "country" || seen[1].Cols != synth.SyntheticFieldName {
		t.Errorf("CHISQ test spec = %+v, want Rows=country Cols=%s", seen[1], synth.SyntheticFieldName)
	}
}

// TestBuildFidelityReport_PerFieldErrorDoesNotAbortReport asserts a
// single field's failing test surfaces as FieldFidelity.Error (Result
// nil) without preventing every other field's entry from appearing.
func TestBuildFidelityReport_PerFieldErrorDoesNotAbortReport(t *testing.T) {
	run := func(physical, presented *encoding.Schema, records []byte, test *types.Test) (*types.TestResult, error) {
		if test.Type == types.TEST_KS {
			return nil, errors.New("boom")
		}
		return &types.TestResult{Type: test.Type, Statistic: 2, PValue: 0.5}, nil
	}

	report := synth.BuildFidelityReport(fidelitySchema(), nil, 1, 1, run)

	if len(report.Fields) != 2 {
		t.Fatalf("len(Fields) = %d, want 2", len(report.Fields))
	}
	ks := report.Fields[0]
	if ks.Result != nil || ks.Error != "boom" {
		t.Errorf("KS entry = %+v, want Result=nil Error=boom", ks)
	}
	chisq := report.Fields[1]
	if chisq.Error != "" || chisq.Result == nil || chisq.Result.Statistic != 2 {
		t.Errorf("CHISQ entry = %+v, want a populated Result and no Error", chisq)
	}
}

// TestSyntheticAsCategoricalSchema_PreservesShapeMapsSyntheticOnly
// asserts the presentation schema keeps every other field untouched
// (same pointer-shared Dictionary for "country") and only widens
// _synthetic to categorical_u8 with a {false:0, true:1} dictionary
// matching packed_bool's own 0/1 on-wire values.
func TestSyntheticAsCategoricalSchema_PreservesShapeMapsSyntheticOnly(t *testing.T) {
	base := fidelitySchema()
	presented := synth.SyntheticAsCategoricalSchema(base)

	if len(presented.Fields) != len(base.Fields) {
		t.Fatalf("field count = %d, want %d", len(presented.Fields), len(base.Fields))
	}
	if presented.Fields[0].Type != encoding.FieldTypeF64 {
		t.Errorf("score type = %v, want unchanged f64", presented.Fields[0].Type)
	}
	if presented.Fields[1].Dictionary != base.Fields[1].Dictionary {
		t.Error("country Dictionary was not preserved by reference")
	}

	tag := presented.Field(synth.SyntheticFieldName)
	if tag == nil {
		t.Fatal("presented schema lost the _synthetic field")
	}
	if !tag.Type.IsCategorical() {
		t.Errorf("_synthetic type = %v, want categorical", tag.Type)
	}
	if got := tag.Dictionary.Resolve(0); got != "false" {
		t.Errorf("_synthetic id 0 = %q, want \"false\"", got)
	}
	if got := tag.Dictionary.Resolve(1); got != "true" {
		t.Errorf("_synthetic id 1 = %q, want \"true\"", got)
	}

	// base is untouched — SyntheticAsCategoricalSchema must not mutate
	// its input in place.
	if base.Field(synth.SyntheticFieldName).Type != encoding.FieldTypePackedBool {
		t.Error("SyntheticAsCategoricalSchema mutated the original schema's _synthetic field type")
	}
}
