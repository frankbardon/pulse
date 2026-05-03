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
			name: "groups force buffered",
			req: &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score"}},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "score"}},
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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanStreamRequest(c.req, c.schema); got != c.want {
				t.Errorf("CanStreamRequest = %v, want %v", got, c.want)
			}
		})
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
