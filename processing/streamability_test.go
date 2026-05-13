package processing

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestCanStreamRequest_RegressionMatrix exercises the exported
// CanStreamRequest hook (used by descriptor/ for predict parity). Each
// row is a (request, schema, want) triple. If predict.Streamable ever
// drifts from runtime behavior, this matrix breaks.
func TestCanStreamRequest_RegressionMatrix(t *testing.T) {
	numericSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
	decimalSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2},
		},
	}
	cases := []struct {
		name   string
		req    *types.Request
		schema *encoding.Schema
		want   bool
	}{
		{
			name:   "online aggregations on numeric stream",
			req:    &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}}},
			schema: numericSchema,
			want:   true,
		},
		{
			name:   "median forces buffered",
			req:    &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "score"}}},
			schema: numericSchema,
			want:   false,
		},
		{
			name: "streamable group + online aggs streams",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "score"}},
			},
			schema: numericSchema,
			want:   true,
		},
		{
			name: "non-streamable group forces buffered",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Groups:       []*types.Group{{Type: types.GROUP_QUANTILE, Field: "score", Interval: 4}},
			},
			schema: numericSchema,
			want:   false,
		},
		{
			name:   "decimal field forces buffered",
			req:    &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}}},
			schema: decimalSchema,
			want:   false,
		},
		{
			name:   "no aggregations forces buffered",
			req:    &types.Request{},
			schema: numericSchema,
			want:   false,
		},
		{
			name: "unpenalized REG_OLS streams",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}}},
			},
			schema: numericSchema,
			want:   true,
		},
		{
			name: "penalized REG_OLS streams (Phase 2)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Penalty: "l2", Alpha: 0.1}},
			},
			schema: numericSchema,
			want:   true,
		},
		{
			name: "l1 penalized REG_OLS streams (Phase 2)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Penalty: "l1", Alpha: 0.1}},
			},
			schema: numericSchema,
			want:   true,
		},
		{
			name: "elasticnet REG_OLS streams (Phase 2)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5}},
			},
			schema: numericSchema,
			want:   true,
		},
		{
			name: "regression with bootstrap modifier forces buffered",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Resample: "bootstrap"}},
			},
			schema: numericSchema,
			want:   false,
		},
		{
			name: "regression GLM forces buffered",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_GLM, Target: "score", Predictors: []string{"score"}, Family: "binomial"}},
			},
			schema: numericSchema,
			want:   false,
		},
		{
			// Phase 3: REG_GLM with poisson+log still routes buffered —
			// IRLS needs multiple passes regardless of family. This
			// exercises the second family wired this phase.
			name: "REG_GLM poisson forces buffered",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_GLM, Target: "score", Predictors: []string{"score"}, Family: "poisson", Link: "log"}},
			},
			schema: numericSchema,
			want:   false,
		},
		{
			name: "REG_BAYES_LINEAR forces buffered (Phase 4)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}}},
			},
			schema: numericSchema,
			want:   false,
		},
		{
			name: "REG_OLS with groupers forces buffered",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}}},
			},
			schema: numericSchema,
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanStreamRequest(c.req, c.schema); got != c.want {
				t.Errorf("CanStreamRequest = %v, want %v", got, c.want)
			}
		})
	}
}

// TestCanStreamRequest_OLSNoPenalty is the dedicated parity check for
// the Phase 1 gate flip: an unpenalized REG_OLS request alongside an
// online aggregation must report true (streaming) at runtime, while
// the same request with a regularization penalty reports false.
// Predict mirrors this via TestPredict_Streamable_MatchesRuntime.
func TestCanStreamRequest_OLSNoPenalty(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
	}

	cases := []struct {
		name string
		spec *types.RegressionSpec
		want bool
	}{
		{
			name: "unpenalized OLS streams",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}},
			want: true,
		},
		{
			name: "l1 penalized OLS streams",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1},
			want: true,
		},
		{
			name: "l2 penalized OLS streams",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l2", Alpha: 0.1},
			want: true,
		},
		{
			name: "elasticnet OLS streams",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y"}},
				Regressions:  []*types.RegressionSpec{c.spec},
			}
			if got := CanStreamRequest(req, schema); got != c.want {
				t.Errorf("CanStreamRequest = %v, want %v", got, c.want)
			}
		})
	}
}

// TestCanStreamRequest_GLMNeverStreams asserts every (family, link)
// combination wired this phase still routes through the buffered path.
// IRLS needs multiple passes to refit the working weights regardless
// of the link function, so streamability must stay false even after
// poisson / gamma land.
func TestCanStreamRequest_GLMNeverStreams(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
	}
	cases := []struct {
		name string
		spec *types.RegressionSpec
	}{
		{"binomial+logit", &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial", Link: "logit"}},
		{"binomial+empty link", &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial"}},
		{"poisson+log", &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "poisson", Link: "log"}},
		{"poisson+empty link", &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "poisson"}},
		{"gamma+inverse", &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "gamma", Link: "inverse"}},
		{"gamma+empty link", &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "gamma"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y"}},
				Regressions:  []*types.RegressionSpec{c.spec},
			}
			if got := CanStreamRequest(req, schema); got {
				t.Errorf("CanStreamRequest = true, want false for REG_GLM")
			}
			if got := c.spec.Streamable(); got {
				t.Errorf("RegressionSpec.Streamable() = true, want false for REG_GLM")
			}
		})
	}
}

// TestRegistryAttributeStreamabilityMatchesTypes asserts that for every
// registered attribute, runtime streamability support (RowLocalAttribute
// for one-pass row-local OR TwoPassAttribute for population-stat
// streaming) matches the type's declared Streamable() value.
func TestRegistryAttributeStreamabilityMatchesTypes(t *testing.T) {
	for _, attrType := range types.AllAttributeTypes() {
		factory, ok := attributeRegistry[attrType]
		if !ok {
			t.Errorf("attribute %s not in registry", attrType)
			continue
		}
		// FORMULA needs a non-empty expression; DATE_PART needs params.
		// Provide minimum viable params so factory succeeds.
		spec := &types.Attribute{Type: attrType, Field: "x"}
		switch attrType {
		case types.ATTR_FORMULA:
			spec.Expression = "1"
		case types.ATTR_DATE_PART:
			spec.Params = []byte(`{"part":"year"}`)
		}
		schema := &encoding.Schema{
			Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeDate}},
		}
		instance, err := factory(spec, schema)
		if err != nil {
			t.Errorf("attribute %s factory error: %v", attrType, err)
			continue
		}
		_, runtimeRowLocal := instance.(RowLocalAttribute)
		_, runtimeTwoPass := instance.(TwoPassAttribute)
		runtimeStreamable := runtimeRowLocal || runtimeTwoPass
		declared := attrType.Streamable()
		if runtimeStreamable != declared {
			t.Errorf("attribute %s: types.Streamable()=%v but runtime RowLocalAttribute=%v TwoPassAttribute=%v — update types/streamability.go to match implementation",
				attrType, declared, runtimeRowLocal, runtimeTwoPass)
		}
	}
}

// TestRegistryStreamabilityMatchesTypes asserts that for every registered
// aggregator, the runtime capability (does the constructed instance
// implement OnlineAggregator?) matches the type's declared Streamable()
// value. This catches drift between types/streamability.go and the
// processing implementations.
func TestRegistryStreamabilityMatchesTypes(t *testing.T) {
	for _, aggType := range types.AllAggregationTypes() {
		factory, ok := aggregatorRegistry[aggType]
		if !ok {
			t.Errorf("aggregator %s not in registry", aggType)
			continue
		}
		instance, err := factory(&types.Aggregation{Type: aggType, Field: "x"}, nil)
		if err != nil {
			t.Errorf("aggregator %s factory error: %v", aggType, err)
			continue
		}
		_, runtimeOnline := instance.(OnlineAggregator)
		declared := aggType.Streamable()
		if runtimeOnline != declared {
			t.Errorf("aggregator %s: types.Streamable()=%v but runtime OnlineAggregator=%v — update types/streamability.go to match implementation",
				aggType, declared, runtimeOnline)
		}
	}
}
