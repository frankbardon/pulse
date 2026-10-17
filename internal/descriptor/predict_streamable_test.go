package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing"
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

	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
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

	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
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
	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
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

	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
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

	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
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
		env := predictFromBytes(data, req, nil)
		result := env.Data.(*descriptor.PredictResult)
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

	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
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

	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
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
	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
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
			env := predictFromBytes(data, req, nil)
			result := env.Data.(*descriptor.PredictResult)
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
			wantCode:    "PULSE_FIELD_TYPE_UNSUPPORTED",
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
			env := predictFromBytes(data, req, nil)
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

// TestPredict_Streamable_EmptyAggregationsBlocks asserts that a request
// with no per-record work (no aggregation, matrix, test or regression)
// is non-streamable (nothing drives UpdateRow).
func TestPredict_Streamable_EmptyAggregationsBlocks(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		},
	}
	data := buildTestPulseFile(t, schema)

	req := &types.Request{}
	env := predictFromBytes(data, req, nil)
	result := env.Data.(*descriptor.PredictResult)
	if result.Streamable {
		t.Error("Streamable = true, want false (no aggregations)")
	}
}

// TestPredict_Streamable_NoAggregationWork (U48 #336): a request whose
// only per-record work is tier-1 tests or regressions streams, in
// predict and at runtime alike — no throwaway AGG_COUNT — while a
// buffered-only test or regression, a test beside a regression, and a
// request with no such work (empty, attributes only) stay buffered.
// Each case pins the expected answer as well as the parity.
func TestPredict_Streamable_NoAggregationWork(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
		{Name: "prior", Type: encoding.FieldTypeF64, Description: "Pre-period score of the same student"},
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: makeDictionary(t, "control", "variant"), Description: "Experiment arm the student was assigned"},
	}}
	ols := func() *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Target: "score", Predictors: []string{"prior"}}
	}
	cases := []struct {
		name string
		req  *types.Request
		want bool
	}{
		{"tests only", &types.Request{Tests: []*types.Test{
			{Type: types.TEST_WELCH, Field: "score", SplitBy: "arm", Params: json.RawMessage(`{"covariate":"prior","reference":"control"}`)},
			{Type: types.TEST_CHISQ, Rows: "arm", Cols: "arm"},
		}}, true},
		{"tests beside a filter and a row-local attribute", &types.Request{
			Filterers:  []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "score > 0"}},
			Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "score / 2", Label: "half"}},
			Tests:      []*types.Test{{Type: types.TEST_T, Field: "half"}},
		}, true},
		{"regression only", &types.Request{Regressions: []*types.RegressionSpec{ols()}}, true},
		{"regression only with multiplicity", &types.Request{Regressions: []*types.RegressionSpec{func() *types.RegressionSpec {
			r := ols()
			r.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
			return r
		}()}}, true},
		{"rank test only buffers", &types.Request{Tests: []*types.Test{{Type: types.TEST_MANN_WHITNEY_U, Field: "score", SplitBy: "arm"}}}, false},
		{"GLM only buffers", &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_GLM, Target: "score", Predictors: []string{"prior"}, Family: "gaussian"}}}, false},
		{"tests beside a regression buffer", &types.Request{Regressions: []*types.RegressionSpec{ols()}, Tests: []*types.Test{{Type: types.TEST_T, Field: "score"}}}, false},
		{"grouped tests only buffer", &types.Request{
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "arm"}},
			Tests:  []*types.Test{{Type: types.TEST_T, Field: "score"}},
		}, false},
		{"tests with a two-pass attribute buffer", &types.Request{
			Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "score", Label: "z"}},
			Tests:      []*types.Test{{Type: types.TEST_T, Field: "z"}},
		}, false},
		{"empty request buffers", &types.Request{}, false},
		{"attributes only buffer", &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "score / 2", Label: "half"}}}, false},
	}
	data := buildTestPulseFile(t, schema)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env := predictFromBytes(data, c.req, nil)
			result := env.Data.(*descriptor.PredictResult)
			runtime := processing.CanStreamRequest(c.req, schema)
			if result.Streamable != runtime || runtime != c.want {
				t.Fatalf("predict.Streamable=%v runtime=%v, want %v (reasons: %v)",
					result.Streamable, runtime, c.want, result.StreamableReasons)
			}
			if c.want && len(result.StreamableReasons) != 0 {
				t.Fatalf("streamable with reasons %v", result.StreamableReasons)
			}
		})
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
	vecSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "a", Type: encoding.FieldTypeF64, Description: "First battery item score"},
			{Name: "b", Type: encoding.FieldTypeF64, Description: "Second battery item score"},
		},
	}
	// fitted builds a request scoring attr type typ over a / b with
	// params, an AGG_SUM over its label, and mutate applied.
	fitted := func(typ types.AttributeType, params string, mutate func(*types.Request)) *types.Request {
		r := &types.Request{
			Attributes:   []*types.Attribute{{Type: typ, Label: "d", Fields: []string{"a", "b"}, Params: json.RawMessage(params)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "s"}},
		}
		if mutate != nil {
			mutate(r)
		}
		return r
	}
	grouped := func(r *types.Request) {
		r.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "a", Interval: 10}}
	}
	cases := []struct {
		name   string
		req    *types.Request
		schema *encoding.Schema
	}{
		// U30 E2-S5: a fitted slot bound to a prebuilt fit (params.fit)
		// is row-local — it streams beside a grouper, a matrix, a
		// feature and a tier-1 test; the same slot folding its fit
		// (fit_on) takes the two-pass drive and buffers there.
		{"fit-bound mahalanobis with a grouper streams", fitted(types.ATTR_MAHALANOBIS, `{"fit":"pop"}`, grouped), vecSchema},
		{"fit_on mahalanobis with a grouper buffers", fitted(types.ATTR_MAHALANOBIS, `{"fit_on":"filtered"}`, grouped), vecSchema},
		{"fit-bound pc score with a grouper streams", fitted(types.ATTR_PC_SCORE, `{"fit":"pop","component":1}`, grouped), vecSchema},
		{"fit-bound factor score with a grouper streams", fitted(types.ATTR_FACTOR_SCORE, `{"fit":"pop","factor":1,"factors":1}`, grouped), vecSchema},
		{"fit-bound vec distance with a grouper streams", fitted(types.ATTR_VEC_DISTANCE, `{"fit":"pop","metric":"mahalanobis","reference":[0,0]}`, grouped), vecSchema},
		{"fitted vec distance with a grouper buffers", fitted(types.ATTR_VEC_DISTANCE, `{"metric":"mahalanobis","reference":[0,0]}`, grouped), vecSchema},
		{"fit-bound mahalanobis beside a matrix streams", fitted(types.ATTR_MAHALANOBIS, `{"fit":"pop"}`, func(r *types.Request) {
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}}}
		}), vecSchema},
		{"fit_on mahalanobis beside a matrix buffers", fitted(types.ATTR_MAHALANOBIS, ``, func(r *types.Request) {
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"a", "b"}}}
		}), vecSchema},
		{"fit-bound mahalanobis beside a tier-1 test streams", fitted(types.ATTR_MAHALANOBIS, `{"fit":"pop"}`, func(r *types.Request) {
			r.Tests = []*types.Test{{Type: types.TEST_T, Field: "a"}}
		}), vecSchema},
		{"online aggregations on numeric", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}}, numericSchema},
		{"median on numeric", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}}}, numericSchema},
		{"sum on decimal", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}}}, decimalSchema},
		{"empty request", &types.Request{}, numericSchema},
		// U46 E3-S1: a grouped request runs every tier-1 test once per
		// group on the buffered path; the same tests ungrouped stream.
		{"ungrouped one-sample TEST_T streams", &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
			Tests:        []*types.Test{{Type: types.TEST_T, Field: "score"}},
		}, numericSchema},
		{"grouped TEST_T buffers", &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
			Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "score", Interval: 10}},
			Tests:        []*types.Test{{Type: types.TEST_T, Field: "score"}},
		}, numericSchema},
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
			"matrix-only MAT_COVARIANCE streams",
			&types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"score"}}}},
			numericSchema,
		},
		{
			"MAT_COVARIANCE beside an online aggregation streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"score"}}},
			},
			numericSchema,
		},
		{
			"MAT_COVARIANCE with a grouper streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "score"}},
				Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"score"}}},
			},
			numericSchema,
		},
		{
			"grouped matrix-only MAT_CORRELATION streams",
			&types.Request{
				Groups:   []*types.Group{{Type: types.GROUP_RANGE, Field: "score", Interval: 10}},
				Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"score"}}},
			},
			numericSchema,
		},
		{
			"MAT_COVARIANCE with a non-streamable grouper buffers",
			&types.Request{
				Groups:   []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score"}},
				Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"score"}}},
			},
			numericSchema,
		},
		{
			"MAT_COVARIANCE with a two-pass attribute buffers",
			&types.Request{
				Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "score", Label: "z"}},
				Matrices:   []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"score"}}},
			},
			numericSchema,
		},
		{
			// Spec-level streamability: params (a rank method) make a
			// built-in type buffered; predict and the runtime read the
			// one types.MatrixSpec.Streamable rule.
			"MAT_CORRELATION with a rank method buffers",
			&types.Request{Matrices: []types.MatrixSpec{
				{Type: types.MAT_CORRELATION, Fields: []string{"score"}},
				{Name: "rank", Type: types.MAT_CORRELATION, Fields: []string{"score"}, Params: json.RawMessage(`{"method":"kendall"}`)},
			}},
			numericSchema,
		},
		{
			"MAT_CORRELATION with method pearson streams",
			&types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"score"}, Params: json.RawMessage(`{"method":"pearson"}`)}}},
			numericSchema,
		},
		{
			"unknown matrix type buffers",
			&types.Request{Matrices: []types.MatrixSpec{{Type: "MAT_NOPE", Fields: []string{"score"}}}},
			numericSchema,
		},
		{
			// Row-local once bound: streams like ATTR_FORMULA, alone and
			// beside a two-pass attribute reading its label.
			"ATTR_REG_PREDICT streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p"}},
				Attributes:   []*types.Attribute{{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)}},
			},
			numericSchema,
		},
		{
			"ATTR_REG_PREDICT beside a two-pass attribute streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "z"}},
				Attributes: []*types.Attribute{
					{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"m"}`)},
					{Type: types.ATTR_ZSCORE, Field: "p", Label: "z"},
				},
			},
			numericSchema,
		},
		// ATTR_FORMULA's total(expr) term takes the two-pass drive
		// (post-aggregation-ratios E1-S3): alone it streams; beside a
		// grouper, feature, regression, tier-1 test or matrix it buffers,
		// exactly like ATTR_ZSCORE. A plain formula stays row-local.
		{"lone total formula streams", ftotReq(nil), numericSchema},
		{"plain formula streams", &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "share"}},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "score / 2", Label: "share"}},
		}, numericSchema},
		{"plain formula with a grouper streams", ftotReq(func(r *types.Request) {
			r.Attributes[0].Expression = "score / 2"
			r.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "score", Interval: 10}}
		}), numericSchema},
		{"total formula with a grouper buffers", ftotReq(func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "score", Interval: 10}}
		}), numericSchema},
		{"total formula with a feature buffers", ftotReq(func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_LOG, Field: "score", Label: "ls"}}
		}), numericSchema},
		{"total formula with a regression buffers", ftotReq(func(r *types.Request) {
			r.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}}}
		}), numericSchema},
		{"total formula with a tier-1 test buffers", ftotReq(func(r *types.Request) {
			r.Tests = []*types.Test{{Type: types.TEST_T, Field: "score"}}
		}), numericSchema},
		{"total formula with a matrix buffers", ftotReq(func(r *types.Request) {
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"score"}}}
		}), numericSchema},
		{
			"TEST_MARDIA buffers",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Tests:        []*types.Test{{Type: types.TEST_MARDIA, Fields: []string{"score"}}},
			},
			numericSchema,
		},
		{
			"TEST_BARTLETT_SPHERICITY streams beside it",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Tests:        []*types.Test{{Type: types.TEST_BARTLETT_SPHERICITY, Fields: []string{"score", "score"}}},
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
			env := predictFromBytes(data, c.req, nil)
			result := env.Data.(*descriptor.PredictResult)
			runtime := processing.CanStreamRequest(c.req, c.schema)
			if result.Streamable != runtime {
				t.Errorf("predict.Streamable=%v but runtime CanStreamRequest=%v (reasons: %v)",
					result.Streamable, runtime, result.StreamableReasons)
			}
		})
	}

	t.Run("buffered components flag", func(t *testing.T) {
		bufferedCases := []struct {
			name       string
			req        *types.Request
			wantSlot   types.AggregationType
			wantBuf    bool
			wantMerge  descriptor.ComponentsMergeability
			wantStream bool
		}{
			{
				name:       "AGG_MEDIAN flags buffered components",
				req:        &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}}},
				wantSlot:   types.AGG_MEDIAN,
				wantBuf:    true,
				wantMerge:  descriptor.None,
				wantStream: false,
			},
			{
				name:       "AGG_PERCENTILE flags buffered components",
				req:        &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_PERCENTILE, Field: "score", Params: json.RawMessage(`{"percentile":0.9}`)}}},
				wantSlot:   types.AGG_PERCENTILE,
				wantBuf:    true,
				wantMerge:  descriptor.None,
				wantStream: false,
			},
			{
				name:       "AGG_SUM mergeable, no buffered flag",
				req:        &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}},
				wantSlot:   types.AGG_SUM,
				wantBuf:    false,
				wantMerge:  descriptor.Mergeable,
				wantStream: true,
			},
			{
				name:       "AGG_MODE_COUNT partial, no buffered flag",
				req:        &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MODE_COUNT, Field: "score"}}},
				wantSlot:   types.AGG_MODE_COUNT,
				wantBuf:    false,
				wantMerge:  descriptor.Partial,
				wantStream: true,
			},
		}
		for _, bc := range bufferedCases {
			t.Run(bc.name, func(t *testing.T) {
				data := buildTestPulseFile(t, numericSchema)
				env := predictFromBytes(data, bc.req, nil)
				result := env.Data.(*descriptor.PredictResult)
				if len(result.Aggregations) != 1 {
					t.Fatalf("expected 1 AggregationPredict entry, got %d", len(result.Aggregations))
				}
				slot := result.Aggregations[0]
				if slot.Type != bc.wantSlot {
					t.Errorf("Aggregations[0].Type = %q, want %q", slot.Type, bc.wantSlot)
				}
				if slot.BufferedComponents != bc.wantBuf {
					t.Errorf("Aggregations[0].BufferedComponents = %v, want %v", slot.BufferedComponents, bc.wantBuf)
				}
				if slot.ComponentSchema.Mergeability != bc.wantMerge {
					t.Errorf("Aggregations[0].ComponentSchema.Mergeability = %q, want %q",
						slot.ComponentSchema.Mergeability, bc.wantMerge)
				}
				// Universal floor: every schema must lead with n + n_null.
				if len(slot.ComponentSchema.Keys) < 2 ||
					slot.ComponentSchema.Keys[0].Name != "n" ||
					slot.ComponentSchema.Keys[1].Name != "n_null" {
					t.Errorf("Aggregations[0].ComponentSchema universal floor missing; keys=%v", slot.ComponentSchema.Keys)
				}
				// The overall Streamable axis must mirror the runtime gate
				// regardless of BufferedComponents — the flag is additive
				// metadata, not a streamability override.
				runtime := processing.CanStreamRequest(bc.req, numericSchema)
				if result.Streamable != runtime {
					t.Errorf("predict.Streamable=%v but runtime CanStreamRequest=%v (reasons: %v)",
						result.Streamable, runtime, result.StreamableReasons)
				}
				if result.Streamable != bc.wantStream {
					t.Errorf("predict.Streamable=%v, want %v (BufferedComponents must not flip the axis)",
						result.Streamable, bc.wantStream)
				}
			})
		}
	})
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

	hasWarning := func(env *descriptor.Envelope, code string) bool {
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
			env := predictFromBytes(data, req, nil)
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

	hasWarning := func(env *descriptor.Envelope, code string) bool {
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
			env := predictFromBytes(data, req, nil)
			got := hasWarning(env, "PROCESSING_REGRESSION_REGULARIZED_SELECTION")
			if got != c.wantWarning {
				t.Errorf("warning fired = %v, want %v; warnings=%v", got, c.wantWarning, env.Warnings)
			}
		})
	}
}

// ftotReq is a streamable request whose one attribute is an ATTR_FORMULA
// calling total(score), mutated by mut when non-nil.
func ftotReq(mut func(*types.Request)) *types.Request {
	r := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "share"}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "score / total(score)", Label: "share"}},
	}
	if mut != nil {
		mut(r)
	}
	return r
}
