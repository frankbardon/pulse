package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_REG_OLS_AcceptsBitPackedTargets asserts that REG_OLS treats
// the two bit-packed integer encodings (u4, packed_bool) as first-class
// numeric targets. Before the IsNumericForAnalytics widening these
// surfaced "regression target field X is not numeric (got T)"
// SERVICE_VALIDATION.
func TestPredict_REG_OLS_AcceptsBitPackedTargets(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Continuous predictor"},
			{Name: "fam", Type: encoding.FieldTypeU4, Nullable: true, Description: "Likert familiarity 0-15"},
			{Name: "subscribed", Type: encoding.FieldTypePackedBool, Description: "Subscription flag"},
			{Name: "opted_in", Type: encoding.FieldTypePackedBool, Nullable: true, Description: "Opt-in flag"},
		},
	}
	data := buildTestPulseFile(t, schema)

	for _, target := range []string{"fam", "subscribed", "opted_in"} {
		t.Run(target, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: target, Predictors: []string{"x"}}},
			}
			env := PredictFromBytes(data, req, nil)
			for _, e := range env.Errors {
				if e.Code == "SERVICE_VALIDATION" {
					t.Fatalf("unexpected SERVICE_VALIDATION error on bit-packed target %q: %s", target, e.Message)
				}
			}
		})
	}
}

// TestPredict_REG_OLS_AcceptsBitPackedPredictors mirrors the target test
// for the predictor slot.
func TestPredict_REG_OLS_AcceptsBitPackedPredictors(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64, Description: "Continuous response"},
			{Name: "fam", Type: encoding.FieldTypeU4, Nullable: true, Description: "Likert familiarity"},
			{Name: "subscribed", Type: encoding.FieldTypePackedBool, Description: "Subscription flag"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}},
		Regressions: []*types.RegressionSpec{{
			Type:       types.REG_OLS,
			Target:     "y",
			Predictors: []string{"fam", "subscribed"},
		}},
	}
	env := PredictFromBytes(data, req, nil)
	for _, e := range env.Errors {
		if e.Code == "SERVICE_VALIDATION" {
			t.Fatalf("unexpected SERVICE_VALIDATION error on bit-packed predictors: %s", e.Message)
		}
	}
}

// TestPredict_REG_GLM_AcceptsBitPackedTarget asserts REG_GLM treats the
// bit-packed integer encodings as valid target / predictor types — the
// textbook pairing for binomial GLM is a packed_bool target.
func TestPredict_REG_GLM_AcceptsBitPackedTarget(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Continuous predictor"},
			{Name: "flag", Type: encoding.FieldTypePackedBool, Description: "Subscription flag"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}},
		Regressions: []*types.RegressionSpec{{
			Type:       types.REG_GLM,
			Target:     "flag",
			Predictors: []string{"x"},
			Family:     "binomial",
		}},
	}
	env := PredictFromBytes(data, req, nil)
	for _, e := range env.Errors {
		if e.Code == "SERVICE_VALIDATION" {
			t.Fatalf("unexpected SERVICE_VALIDATION on REG_GLM binomial + packed_bool target: %s", e.Message)
		}
	}
}

// TestPredict_REG_BAYES_LINEAR_AcceptsBitPackedTarget mirrors the GLM
// test for the Bayesian engine.
func TestPredict_REG_BAYES_LINEAR_AcceptsBitPackedTarget(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Continuous predictor"},
			{Name: "score", Type: encoding.FieldTypeU4, Nullable: true, Description: "Likert score"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x"}},
		Regressions: []*types.RegressionSpec{{
			Type:       types.REG_BAYES_LINEAR,
			Target:     "score",
			Predictors: []string{"x"},
		}},
	}
	env := PredictFromBytes(data, req, nil)
	for _, e := range env.Errors {
		if e.Code == "SERVICE_VALIDATION" {
			t.Fatalf("unexpected SERVICE_VALIDATION on REG_BAYES_LINEAR + u4 target: %s", e.Message)
		}
	}
}

// TestIsNumericForAnalytics asserts the predicate exposes the broader
// analytics-layer numeric set: existing integer/float/decimal types plus
// the bit-packed integer encodings (u4, packed_bool) and date.
func TestIsNumericForAnalytics(t *testing.T) {
	includes := []encoding.FieldType{
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate,
		encoding.FieldTypeDecimal128,
		encoding.FieldTypeU4, encoding.FieldTypePackedBool,
	}
	for _, ft := range includes {
		if !ft.IsNumericForAnalytics() {
			t.Errorf("expected %s to be IsNumericForAnalytics()=true", ft.String())
		}
	}
	excludes := []encoding.FieldType{
		encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32,
	}
	for _, ft := range excludes {
		if ft.IsNumericForAnalytics() {
			t.Errorf("expected %s to be IsNumericForAnalytics()=false", ft.String())
		}
	}
}

// TestIsNumeric asserts the narrow numeric predicate covers only the
// strict scalar family (int / float / decimal). Bit-packed integer
// encodings and date are deliberately excluded.
func TestIsNumeric(t *testing.T) {
	includes := []encoding.FieldType{
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDecimal128,
	}
	for _, ft := range includes {
		if !ft.IsNumeric() {
			t.Errorf("expected %s to be IsNumeric()=true", ft.String())
		}
	}
	excludes := []encoding.FieldType{
		encoding.FieldTypeDate,
		encoding.FieldTypeU4, encoding.FieldTypePackedBool,
		encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32,
	}
	for _, ft := range excludes {
		if ft.IsNumeric() {
			t.Errorf("expected %s to be IsNumeric()=false", ft.String())
		}
	}
}
