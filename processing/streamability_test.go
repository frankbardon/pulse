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
			name: "REG_BAYES_LINEAR streams (Phase 4)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}}},
			},
			schema: numericSchema,
			want:   true,
		},
		{
			name: "REG_BAYES_LINEAR with bootstrap modifier forces buffered",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_BAYES_LINEAR, Target: "score", Predictors: []string{"score"}, Resample: "bootstrap"}},
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
		{
			name: "REG_OLS jackknife forces buffered (Phase 5)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Resample: "jackknife"}},
			},
			schema: numericSchema,
			want:   false,
		},
		{
			name: "REG_OLS stepwise selection forces buffered (Phase 5)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Selection: "stepwise", Criterion: "aic"}},
			},
			schema: numericSchema,
			want:   false,
		},
		{
			name: "REG_OLS forward selection forces buffered (Phase 5)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Selection: "forward", Criterion: "bic"}},
			},
			schema: numericSchema,
			want:   false,
		},
		{
			name: "REG_OLS regularized + jackknife forces buffered (Phase 5)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}, Penalty: "l1", Alpha: 0.1, Resample: "jackknife"}},
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

// TestCanStreamRequest_ModifiersBuffered asserts every Resample and
// Selection value downgrades streamability to the buffered path,
// regardless of the base regression type's own Streamable() value.
// Bayes + modifier combos are excluded because they reject at spec
// validation (PROCESSING_CONFIG) before reaching the streamability
// gate.
func TestCanStreamRequest_ModifiersBuffered(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "y", Type: encoding.FieldTypeF64},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
	}
	bases := []struct {
		name string
		spec types.RegressionSpec
	}{
		{name: "OLS no penalty", spec: types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}},
		{name: "OLS l1", spec: types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1}},
		{name: "OLS l2", spec: types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l2", Alpha: 0.1}},
		{name: "OLS elasticnet", spec: types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5}},
		{name: "GLM binomial", spec: types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial"}},
		{name: "GLM poisson", spec: types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "poisson"}},
		{name: "BAYES_LINEAR", spec: types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}}},
	}
	modifiers := []struct {
		name string
		mod  func(s *types.RegressionSpec)
	}{
		{name: "jackknife", mod: func(s *types.RegressionSpec) { s.Resample = "jackknife" }},
		{name: "bootstrap", mod: func(s *types.RegressionSpec) { s.Resample = "bootstrap" }},
		{name: "forward+aic", mod: func(s *types.RegressionSpec) { s.Selection = "forward"; s.Criterion = "aic" }},
		{name: "backward+bic", mod: func(s *types.RegressionSpec) { s.Selection = "backward"; s.Criterion = "bic" }},
		{name: "stepwise+aic", mod: func(s *types.RegressionSpec) { s.Selection = "stepwise"; s.Criterion = "aic" }},
	}
	for _, base := range bases {
		for _, m := range modifiers {
			t.Run(base.name+"/"+m.name, func(t *testing.T) {
				spec := base.spec
				m.mod(&spec)
				req := &types.Request{
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "y"}},
					Regressions:  []*types.RegressionSpec{&spec},
				}
				if got := CanStreamRequest(req, schema); got {
					t.Errorf("CanStreamRequest = true; want false for modifier-bearing spec")
				}
				if got := spec.Streamable(); got {
					t.Errorf("RegressionSpec.Streamable() = true; want false for modifier-bearing spec")
				}
			})
		}
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
		// FORMULA needs a non-empty expression; DATE_PART needs params;
		// ATTR_REG_* need Target + Predictors. Provide minimum viable
		// params so factory succeeds.
		spec := &types.Attribute{Type: attrType, Field: "x"}
		schema := &encoding.Schema{
			Fields: []encoding.Field{
				{Name: "x", Type: encoding.FieldTypeDate},
				{Name: "y", Type: encoding.FieldTypeF64},
				{Name: "p1", Type: encoding.FieldTypeF64},
			},
		}
		switch attrType {
		case types.ATTR_FORMULA:
			spec.Expression = "1"
		case types.ATTR_DATE_PART:
			spec.Params = []byte(`{"part":"year"}`)
		case types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE:
			spec.Target = "y"
			spec.Predictors = []string{"p1"}
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
	// Aggregators that require non-empty Params at construction time.
	// Supply matching JSON so the factory does not fail before the
	// streamability assertion runs.
	paramsByType := map[types.AggregationType]string{
		types.AGG_WEIGHTED_MEAN: `{"weight_field":"w"}`,
		types.AGG_RATIO:         `{"numerator_field":"num","denominator_field":"den"}`,
	}
	for _, aggType := range types.AllAggregationTypes() {
		factory, ok := aggregatorRegistry[aggType]
		if !ok {
			t.Errorf("aggregator %s not in registry", aggType)
			continue
		}
		spec := &types.Aggregation{Type: aggType, Field: "x"}
		if raw, ok := paramsByType[aggType]; ok {
			spec.Params = []byte(raw)
		}
		instance, err := factory(spec, nil)
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
