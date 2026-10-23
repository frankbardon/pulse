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
// buffered-only test or regression, a grouped request, and a request
// with no such work (empty, attributes only) stay buffered. Tests
// beside a streamable regression stream too (the one-request readout).
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
		// U48 E3-S3 fix: the whole readout in one request streams.
		{"tests beside a regression", &types.Request{Regressions: []*types.RegressionSpec{ols()}, Tests: []*types.Test{{Type: types.TEST_T, Field: "score"}}}, true},
		{"weighted tests beside a regression with multiplicity", &types.Request{
			Weight:       &types.WeightSpec{Field: "prior", Kind: types.WeightKindFrequency},
			Multiplicity: &types.Multiplicity{Method: types.MultiplicityMethodHolm},
			Regressions: []*types.RegressionSpec{func() *types.RegressionSpec {
				r := ols()
				r.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
				return r
			}()},
			Tests: []*types.Test{{Type: types.TEST_WELCH, Field: "score", SplitBy: "arm", Params: json.RawMessage(`{"covariate":"prior","reference":"control"}`)}},
		}, true},
		{"tests beside a robust regression buffer", &types.Request{Regressions: []*types.RegressionSpec{func() *types.RegressionSpec {
			r := ols()
			r.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
			return r
		}()}, Tests: []*types.Test{{Type: types.TEST_T, Field: "score"}}}, false},
		{"grouped tests beside a regression buffer", &types.Request{
			Groups:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "arm"}},
			Regressions: []*types.RegressionSpec{ols()},
			Tests:       []*types.Test{{Type: types.TEST_T, Field: "score"}},
		}, false},
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
	teDict := encoding.NewDictionary()
	teDict.Add("a")
	teDict.Add("b")
	teSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: teDict, Description: "Loan purpose category"},
			{Name: "y", Type: encoding.FieldTypeF64, Description: "Default outcome indicator"},
			{Name: "s", Type: encoding.FieldTypeU8, Description: "Stored training flag"},
		},
	}
	// teReq target-encodes g with params, sums the encoding, mutate applied.
	teReq := func(params string, mutate func(*types.Request)) *types.Request {
		r := &types.Request{
			Features:     []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "g", Label: "te", Params: json.RawMessage(params)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "te", Label: "s_te"}},
		}
		if mutate != nil {
			mutate(r)
		}
		return r
	}
	// regScored builds an ATTR_REG_FITTED / RESIDUAL slot: params.fit
	// alone, or (params empty) the OLS refit of b on a.
	regScored := func(typ types.AttributeType, params string, mutate func(*types.Request)) *types.Request {
		attr := &types.Attribute{Type: typ, Label: "d", Params: json.RawMessage(params)}
		if params == "" {
			attr.Target, attr.Predictors = "b", []string{"a"}
		}
		r := &types.Request{
			Attributes:   []*types.Attribute{attr},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "s"}},
		}
		if mutate != nil {
			mutate(r)
		}
		return r
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
		// U53 E2-S3: an ATTR_REG_FITTED / RESIDUAL slot naming a bound
		// fit (params.fit) is row-local and streams beside a grouper;
		// the same slot refitting OLS takes the two-pass drive.
		{"fit-bound reg residual with a grouper streams", regScored(types.ATTR_REG_RESIDUAL, `{"fit":"m"}`, grouped), vecSchema},
		{"fit-bound reg fitted with a grouper streams", regScored(types.ATTR_REG_FITTED, `{"fit":"m"}`, grouped), vecSchema},
		{"refit reg residual with a grouper buffers", regScored(types.ATTR_REG_RESIDUAL, ``, grouped), vecSchema},
		{"online aggregations on numeric", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}}, numericSchema},
		// U51 E3-S3: a GROUP_ROUNDED grouping streams (and now merges).
		{"grouped GROUP_ROUNDED sum streams", &types.Request{
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
			Groups:       []*types.Group{{Type: types.GROUP_ROUNDED, Field: "score", Interval: 2.5}},
		}, numericSchema},
		{"median on numeric", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}}}, numericSchema},
		// model-workbench E2-S1: AGG_AUC sorts every scored row — buffered,
		// alone and beside a streamable slot.
		{"AUC buffers", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_AUC, Field: "score", Params: json.RawMessage(`{"outcome":"score"}`)}}}, numericSchema},
		{"AUC beside a sum buffers", &types.Request{Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score"},
			{Type: types.AGG_AUC, Field: "score", Params: json.RawMessage(`{"outcome":"score"}`)}}}, numericSchema},
		{"sum on decimal", &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}}}, decimalSchema},
		{"empty request", &types.Request{}, numericSchema},
		{"adstock window buffers", &types.Request{Windows: []*types.Window{{
			Type: types.WIN_ADSTOCK, Field: "score", Label: "ad", OrderBy: []types.OrderKey{{Field: "score"}},
			Params: json.RawMessage(`{"kernel":"weibull_cdf","shape":1.5,"scale":2,"max_lag":3}`),
		}}}, numericSchema},
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
			// U48 E3-S3 fix: tier-1 tests beside a regression stream.
			"REG_OLS beside a tier-1 test streams",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}}},
				Tests:        []*types.Test{{Type: types.TEST_T, Field: "score"}},
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
		// model-workbench E3-S3: a target encoder's pre-pass reads its
		// field, target and fit_on from the raw record — a stored column
		// streams, an earlier feature's column buffers.
		{"target encode streams", teReq(`{"target":"y"}`, nil), teSchema},
		{"target encode fit_on stored streams", teReq(`{"target":"y","fit_on":"s == 0","folds":3,"rng_seed":1}`, nil), teSchema},
		{"target encode fit_on derived buffers", teReq(`{"target":"y","fit_on":"split == 0"}`, func(r *types.Request) {
			r.Features = append([]*types.Feature{{Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "split", Params: json.RawMessage(`{"ratios":[0.5,0.5]}`)}}, r.Features...)
		}), teSchema},
		{"target encode derived target buffers", teReq(`{"target":"ly"}`, func(r *types.Request) {
			r.Features = append([]*types.Feature{{Type: types.FEAT_LOG, Field: "y", Label: "ly"}}, r.Features...)
		}), teSchema},
		{"bucketize quantiles of a stored column streams", teReq(`{"target":"y"}`, func(r *types.Request) {
			r.Features = []*types.Feature{{Type: types.FEAT_BUCKETIZE, Field: "y", Label: "b", Params: json.RawMessage(`{"quantiles":4}`)}}
			r.Aggregations[0].Field = "b"
		}), teSchema},
		{"bucketize quantiles of a derived column buffers", teReq(`{"target":"y"}`, func(r *types.Request) {
			r.Features = []*types.Feature{
				{Type: types.FEAT_LOG, Field: "y", Label: "ly"},
				{Type: types.FEAT_BUCKETIZE, Field: "ly", Label: "b", Params: json.RawMessage(`{"quantiles":4}`)},
			}
			r.Aggregations[0].Field = "b"
		}), teSchema},
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
		// U57: a holdout scores its held-out rows after the fit — a
		// second pass, so the slot buffers on both sides.
		{
			"REG_OLS with a holdout buffers",
			&types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Holdout: "score > 1"}},
			},
			numericSchema,
		},
		{
			"REG_BAYES_LINEAR with a holdout buffers",
			&types.Request{
				Regressions: []*types.RegressionSpec{{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}, Holdout: "score > 1"}},
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
		// U55: buffered only (it orders every record of a subject) —
		// grouped, beside an expression filter reading it by name.
		{
			"ATTR_PREVIOUS_STATE buffers",
			&types.Request{
				Attributes:   []*types.Attribute{{Type: types.ATTR_PREVIOUS_STATE, Field: "plan", SubjectField: "id", OrderField: "seen", Label: "prev"}},
				Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `prev == "paid"`}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "plan"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan", Label: "n"}},
			},
			prevStatePredictSchema(),
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
				name:       "AGG_AUC flags buffered components",
				req:        &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_AUC, Field: "score", Params: json.RawMessage(`{"outcome":"score"}`)}}},
				wantSlot:   types.AGG_AUC,
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
