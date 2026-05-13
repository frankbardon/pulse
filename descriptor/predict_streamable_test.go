package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// TestPredict_Streamable_OnlineAggregations confirms that a request with
// only online aggregations on numeric fields reports streamable=true with
// no reasons.
func TestPredict_Streamable_OnlineAggregations(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score"},
			{Type: types.AGG_AVERAGE, Field: "score"},
			{Type: types.AGG_COUNT, Field: "score"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if !result.Streamable {
		t.Errorf("Streamable = false, want true. Reasons: %v", result.StreamableReasons)
	}
	if len(result.StreamableReasons) != 0 {
		t.Errorf("expected no reasons, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_StreamableGroupAllowed: GROUP_CATEGORY with
// online aggregators reports streamable=true.
func TestPredict_Streamable_StreamableGroupAllowed(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
			{Name: "grade", Type: encoding.FieldTypeCategoricalU8, Description: "Letter grade for the student", Dictionary: makeDictionary(t, "A", "B", "C")},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if !result.Streamable {
		t.Errorf("Streamable = false, want true. Reasons: %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_NonStreamableGroupBlocks: GROUP_QUANTILE forces
// buffered.
func TestPredict_Streamable_NonStreamableGroupBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}},
		Groups:       []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4}},
	}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false (QUANTILE not streamable)")
	}
	if !containsReason(result.StreamableReasons, "GROUP_QUANTILE") {
		t.Errorf("expected reason mentioning GROUP_QUANTILE, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_NonOnlineAggBlocksStreaming asserts MEDIAN forces
// buffered with a per-aggregation reason.
func TestPredict_Streamable_NonOnlineAggBlocksStreaming(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false")
	}
	if !containsReason(result.StreamableReasons, "AGG_MEDIAN") {
		t.Errorf("expected reason mentioning AGG_MEDIAN, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_PercentileAttributeBlocks asserts ATTR_PERCENTILE
// (the only remaining buffered-only attribute) forces buffered.
func TestPredict_Streamable_PercentileAttributeBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "score"}},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false")
	}
	if !containsReason(result.StreamableReasons, "ATTR_PERCENTILE") {
		t.Errorf("expected reason mentioning ATTR_PERCENTILE, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_TwoPassAttributeAllowed: ZSCORE/TSCORE/NORMALIZED
// implement TwoPassAttribute and report streamable=true.
func TestPredict_Streamable_TwoPassAttributeAllowed(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	for _, attrType := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED} {
		req := &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
			Attributes:   []*types.Attribute{{Type: attrType, Field: "score"}},
		}
		env := PredictFromBytes(data, req, nil)
		result := env.Data.(*PredictResult)
		if !result.Streamable {
			t.Errorf("%s: Streamable = false, want true. Reasons: %v", attrType, result.StreamableReasons)
		}
	}
}

// TestPredict_Streamable_RowLocalAttributesAllowed: FORMULA + DATE_PART
// are row-local; predict should report Streamable=true even with them
// present (provided no other gates fire).
func TestPredict_Streamable_RowLocalAttributesAllowed(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: "score * 2", Label: "doubled"},
		},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if !result.Streamable {
		t.Errorf("Streamable = false, want true. Reasons: %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_DecimalFieldBlocks asserts decimal128 fields
// force buffered even with online aggregations.
func TestPredict_Streamable_DecimalFieldBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Transaction amount in USD cents"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}},
	}

	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false")
	}
	if !containsReason(result.StreamableReasons, "decimal") {
		t.Errorf("expected reason mentioning decimal, got %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_UnpenalizedOLSStreams asserts the Phase 1
// gate flip: REG_OLS with Penalty == "" and no Resample / Selection
// modifiers reports Streamable=true with no blocking reasons.
func TestPredict_Streamable_UnpenalizedOLSStreams(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64, Description: "Response variable values"},
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Predictor variable values"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y"}},
		Regressions: []*types.RegressionSpec{
			{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}},
		},
	}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if !result.Streamable {
		t.Errorf("Streamable = false, want true. Reasons: %v", result.StreamableReasons)
	}
}

// TestPredict_Streamable_RegressionBlocks asserts the regression
// variants that do not stream today (GLM, modifier-wrapped specs) still
// force Streamable=false. After Phase 4, REG_BAYES_LINEAR without
// modifiers streams; modifier-wrapped Bayes specs remain buffered.
func TestPredict_Streamable_RegressionBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64, Description: "Response variable values"},
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Predictor variable values"},
		},
	}
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		spec *types.RegressionSpec
	}{
		{"GLM (Phase 3)", &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial"}},
		{"OLS with bootstrap (Phase 5)", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Resample: "bootstrap"}},
		{"OLS with jackknife (Phase 5)", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Resample: "jackknife"}},
		{"OLS with selection (Phase 5)", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Selection: "stepwise", Criterion: "aic"}},
		{"Bayes linear with bootstrap (Phase 5)", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Resample: "bootstrap"}},
		{"Bayes linear with stepwise selection (Phase 5)", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Selection: "stepwise", Criterion: "aic"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y"}},
				Regressions:  []*types.RegressionSpec{c.spec},
			}
			env := PredictFromBytes(data, req, nil)
			result := env.Data.(*PredictResult)
			if result.Streamable {
				t.Errorf("Streamable = true, want false. Reasons: %v", result.StreamableReasons)
			}
			if !containsReason(result.StreamableReasons, "regression") {
				t.Errorf("expected a reason mentioning regression, got %v", result.StreamableReasons)
			}
		})
	}
}

// TestPredict_RegressionValidation asserts validateRegressions catches
// the structural mistakes that don't need the engine: unknown types,
// missing target / predictors, non-numeric fields, missing GLM Family,
// and Selection-without-Criterion.
func TestPredict_RegressionValidation(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Numeric score"},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Region category"},
		},
	}
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name        string
		regressions []*types.RegressionSpec
		wantCode    string
	}{
		{
			name:        "unknown type",
			regressions: []*types.RegressionSpec{{Type: types.RegressionType("REG_NOT_A_TYPE"), Target: "score", Predictors: []string{"score"}}},
			wantCode:    "SERVICE_VALIDATION",
		},
		{
			name:        "missing target",
			regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Predictors: []string{"score"}}},
			wantCode:    "SERVICE_VALIDATION",
		},
		{
			name:        "missing predictors",
			regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score"}},
			wantCode:    "SERVICE_VALIDATION",
		},
		{
			name:        "categorical predictor",
			regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"region"}}},
			wantCode:    "SERVICE_VALIDATION",
		},
		{
			name:        "glm missing family",
			regressions: []*types.RegressionSpec{{Type: types.REG_GLM, Target: "score", Predictors: []string{"score"}}},
			wantCode:    "SERVICE_VALIDATION",
		},
		{
			name:        "glm invalid family",
			regressions: []*types.RegressionSpec{{Type: types.REG_GLM, Target: "score", Predictors: []string{"score"}, Family: "bogus"}},
			wantCode:    "PROCESSING_REGRESSION_INVALID_FAMILY",
		},
		{
			name:        "selection without criterion",
			regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Selection: "stepwise"}},
			wantCode:    "SERVICE_VALIDATION",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  c.regressions,
			}
			env := PredictFromBytes(data, req, nil)
			found := false
			for _, e := range env.Errors {
				if e.Code == c.wantCode {
					found = true
					break
				}
			}
			if !found {
				codes := make([]string, 0, len(env.Errors))
				for _, e := range env.Errors {
					codes = append(codes, e.Code)
				}
				t.Errorf("expected error code %q, got envelope codes %v", c.wantCode, codes)
			}
		})
	}
}

// TestPredict_Streamable_EmptyAggregationsBlocks asserts that requests
// with no aggregations are non-streamable (no work to drive UpdateRow).
func TestPredict_Streamable_EmptyAggregationsBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{}
	env := PredictFromBytes(data, req, nil)
	result := env.Data.(*PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false (no aggregations)")
	}
}

// TestPredict_Streamable_MatchesRuntime asserts predict's Streamable flag
// agrees with processing.CanStreamRequest for a representative matrix.
// This is the cross-package parity gate: drift in either side breaks it.
func TestPredict_Streamable_MatchesRuntime(t *testing.T) {
	numericSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	decimalSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Transaction amount in USD cents"},
		},
	}
	geoSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "loc", Type: encoding.FieldTypePointF64, Description: "Latitude/longitude pair (degrees)"},
		},
	}
	cases := []struct {
		name   string
		req    *types.Request
		schema *encoding.Schema
	}{
		{"online aggregations on numeric", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}}, numericSchema},
		{"median on numeric", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}}}, numericSchema},
		{"sum on decimal", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}}}, decimalSchema},
		{"empty request", &types.Request{}, numericSchema},
		{"geo centroid", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_GEO_CENTROID, Field: "loc"}}}, geoSchema},
		{
			"unpenalized REG_OLS streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}}},
			},
			numericSchema,
		},
		{
			"penalized REG_OLS streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Penalty: "l2", Alpha: 0.1}},
			},
			numericSchema,
		},
		{
			"l1 REG_OLS streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Penalty: "l1", Alpha: 0.1}},
			},
			numericSchema,
		},
		{
			"elasticnet REG_OLS streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5}},
			},
			numericSchema,
		},
		{
			"REG_GLM buffers",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_GLM, Target: "score", Predictors: []string{"score"}, Family: "binomial"}},
			},
			numericSchema,
		},
		{
			"REG_BAYES_LINEAR streams (Phase 4)",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}}},
			},
			numericSchema,
		},
		{
			"REG_BAYES_LINEAR with bootstrap modifier buffers",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}, Resample: "bootstrap"}},
			},
			numericSchema,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := buildTestPulseFile(t, c.schema)
			env := PredictFromBytes(data, c.req, nil)
			result := env.Data.(*PredictResult)
			runtime := processing.CanStreamRequest(c.req, c.schema)
			if result.Streamable != runtime {
				t.Errorf("predict.Streamable=%v but runtime CanStreamRequest=%v (reasons: %v)",
					result.Streamable, runtime, result.StreamableReasons)
			}
		})
	}
}

func containsReason(reasons []string, needle string) bool {
	for _, r := range reasons {
		if strings.Contains(r, needle) {
			return true
		}
	}
	return false
}

// TestPredict_RegressionApproximateSEWarning asserts the L1 / elasticnet
// approximate-SE warning fires on a regularized OLS spec when no
// Resample modifier is set, and is suppressed when Resample is set
// (bootstrap / jackknife replaces the analytical SE entirely).
func TestPredict_RegressionApproximateSEWarning(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64, Description: "Response variable values"},
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Predictor variable values"},
		},
	}
	data := buildTestPulseFile(t, schema)

	hasWarning := func(env *Envelope, code string) bool {
		for _, w := range env.Warnings {
			if w.Code == code {
				return true
			}
		}
		return false
	}

	cases := []struct {
		name        string
		spec        *types.RegressionSpec
		wantWarning bool
	}{
		{"l1 no resample emits warning", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1}, true},
		{"elasticnet no resample emits warning", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5}, true},
		{"l1 with bootstrap suppresses warning", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1, Resample: "bootstrap"}, false},
		{"l1 with jackknife suppresses warning", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1, Resample: "jackknife"}, false},
		{"l2 never emits warning", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l2", Alpha: 0.1}, false},
		{"unpenalized never emits warning", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y"}},
				Regressions:  []*types.RegressionSpec{c.spec},
			}
			env := PredictFromBytes(data, req, nil)
			got := hasWarning(env, "PROCESSING_REGRESSION_APPROXIMATE_SE")
			if got != c.wantWarning {
				t.Errorf("warning fired = %v, want %v; warnings=%v", got, c.wantWarning, env.Warnings)
			}
		})
	}
}

// TestPredict_RegressionRegularizedSelectionWarning asserts the
// regularized+selection warning fires when REG_OLS combines a
// non-empty Penalty with a non-empty Selection.
func TestPredict_RegressionRegularizedSelectionWarning(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64, Description: "Response variable values"},
			{Name: "x", Type: encoding.FieldTypeF64, Description: "Predictor variable values"},
		},
	}
	data := buildTestPulseFile(t, schema)

	hasWarning := func(env *Envelope, code string) bool {
		for _, w := range env.Warnings {
			if w.Code == code {
				return true
			}
		}
		return false
	}

	cases := []struct {
		name        string
		spec        *types.RegressionSpec
		wantWarning bool
	}{
		{"l2 + forward warns", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l2", Alpha: 0.1, Selection: "forward", Criterion: "aic"}, true},
		{"l1 + stepwise warns", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1, Selection: "stepwise", Criterion: "bic"}, true},
		{"elasticnet + backward warns", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5, Selection: "backward", Criterion: "aic"}, true},
		{"unpenalized + forward no warning", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Selection: "forward", Criterion: "aic"}, false},
		{"penalized no selection no warning", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l2", Alpha: 0.1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y"}},
				Regressions:  []*types.RegressionSpec{c.spec},
			}
			env := PredictFromBytes(data, req, nil)
			got := hasWarning(env, "PROCESSING_REGRESSION_REGULARIZED_SELECTION")
			if got != c.wantWarning {
				t.Errorf("warning fired = %v, want %v; warnings=%v", got, c.wantWarning, env.Warnings)
			}
		})
	}
}
