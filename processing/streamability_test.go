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
			name: "regression slot forces buffered (Phase 0)",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "score", Predictors: []string{"score"}}},
			},
			schema: numericSchema,
			want:   false,
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanStreamRequest(c.req, c.schema); got != c.want {
				t.Errorf("CanStreamRequest = %v, want %v", got, c.want)
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
