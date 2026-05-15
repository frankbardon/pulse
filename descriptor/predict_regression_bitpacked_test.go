package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_REG_OLS_AcceptsBitPackedTargets asserts that REG_OLS treats
// the three bit-packed integer encodings (nullable_u4, nullable_bool,
// packed_bool) as first-class numeric targets. Before the
// IsNumericForAnalytics widening these surfaced
// "regression target field X is not numeric (got T)" SERVICE_VALIDATION.
func TestPredict_REG_OLS_AcceptsBitPackedTargets(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Continuous predictor"},
			{Name: "fam", Type: encoding.FieldTypeNullableU4, Description: "Likert familiarity 0-7 with 15 as null sentinel"},
			{Name: "subscribed", Type: encoding.FieldTypePackedBool, Description: "Subscription flag"},
			{Name: "opted_in", Type: encoding.FieldTypeNullableBool, Description: "Opt-in flag"},
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
			{Name: "fam", Type: encoding.FieldTypeNullableU4, Description: "Likert familiarity"},
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

// TestPredict_REG_GLM_StillRejectsBitPacked confirms the widening was
// surgical: REG_GLM / REG_BAYES_LINEAR keep the narrower legacy numeric
// set until their fit paths are deliberately widened.
func TestPredict_REG_GLM_StillRejectsBitPacked(t *testing.T) {
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
	found := false
	for _, e := range env.Errors {
		if e.Code == "SERVICE_VALIDATION" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected REG_GLM to keep rejecting packed_bool target; got %d errors", len(env.Errors))
	}
}

// TestIsNumericForAnalytics asserts the predicate exposes the broader
// analytics-layer numeric set: existing integer/float/decimal types plus
// the three bit-packed integer encodings.
func TestIsNumericForAnalytics(t *testing.T) {
	includes := []encoding.FieldType{
		encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeDate,
		encoding.FieldTypeDecimal128, encoding.FieldTypeNullableDecimal128,
		encoding.FieldTypeNullableU4, encoding.FieldTypeNullableU8, encoding.FieldTypeNullableU16,
		encoding.FieldTypeNullableBool, encoding.FieldTypePackedBool,
	}
	for _, ft := range includes {
		if !ft.IsNumericForAnalytics() {
			t.Errorf("expected %s to be IsNumericForAnalytics()=true", ft.String())
		}
	}
	excludes := []encoding.FieldType{
		encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32,
		encoding.FieldTypePointF64, encoding.FieldTypeH3Cell,
	}
	for _, ft := range excludes {
		if ft.IsNumericForAnalytics() {
			t.Errorf("expected %s to be IsNumericForAnalytics()=false", ft.String())
		}
	}
}
