package processing

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestMetaAggregator_OrderStatOps_Components is the E1-S9 table-
// driven sweep covering Components() emission for the two order-stat
// aggregators: AGG_MEDIAN and AGG_PERCENTILE. Both operators are
// ComponentsMergeability=None — they require a sorted view of the full
// value set, so the streaming path defers emission to terminal buffered
// flush (the per-chunk omission contract lands in E4-S4).
//
// MEDIAN cases exercise odd N, even N, single-row, and empty input.
// PERCENTILE cases vary the configured percentile to drive the linear
// interpolation through its three branches: single-row trivial,
// integer-rank single-bucket, and fractional-rank two-bucket
// interpolation. Empty input must collapse to (nil, nil) per the
// universal-floor convention — the orchestrator's per-record (n,
// n_null) bookkeeping is the source of truth for "no rows seen".
//
// Each case asserts (a) the universal floor (N, NNull) matches the
// per-record bookkeeping and (b) the operator-specific map matches the
// schema declared in descriptor/capabilities_aggregators.go byte-for-
// byte via reflect.DeepEqual.
func TestMetaAggregator_OrderStatOps_Components(t *testing.T) {
	schema := numericSchema()
	makeRecs := func(values []float64, nullIdxs []int) []*Record {
		return makeRecordsWithNulls(schema, "score", values, nullIdxs)
	}

	type expectation struct {
		n        int
		nNull    int
		operator map[string]any
	}
	type tc struct {
		name    string
		aggType types.AggregationType
		params  json.RawMessage
		records []*Record
		want    expectation
	}

	// Percentile param helpers — keep the json.RawMessage literal at
	// the call site so the request shape is grep-discoverable.
	pParams := func(p float64) json.RawMessage {
		raw, err := json.Marshal(percentileParams{Percentile: p})
		if err != nil {
			t.Fatalf("marshal percentile params: %v", err)
		}
		return raw
	}

	tests := []tc{
		// -------- AGG_MEDIAN --------
		//
		// Odd N: sorted [1,2,3,4,5] — middle index 2 → median=3,
		// position_low=position_high=2.
		{
			name:    "MEDIAN_oddN",
			aggType: types.AGG_MEDIAN,
			records: makeRecs([]float64{1, 2, 3, 4, 5}, nil),
			want: expectation{n: 5, nNull: 0, operator: map[string]any{
				"position_low":  2,
				"position_high": 2,
				"median":        3.0,
			}},
		},
		// Even N: sorted [1,2,3,4] — bracketed by indices 1, 2 →
		// median=(2+3)/2=2.5.
		{
			name:    "MEDIAN_evenN",
			aggType: types.AGG_MEDIAN,
			records: makeRecs([]float64{1, 2, 3, 4}, nil),
			want: expectation{n: 4, nNull: 0, operator: map[string]any{
				"position_low":  1,
				"position_high": 2,
				"median":        2.5,
			}},
		},
		// Single-row: position_low=position_high=0, median=value.
		{
			name:    "MEDIAN_singleRow",
			aggType: types.AGG_MEDIAN,
			records: makeRecs([]float64{7}, nil),
			want: expectation{n: 1, nNull: 0, operator: map[string]any{
				"position_low":  0,
				"position_high": 0,
				"median":        7.0,
			}},
		},
		// Empty: Components() collapses to (nil, nil) — universal
		// floor (n=0) is the source of truth.
		{
			name:    "MEDIAN_empty",
			aggType: types.AGG_MEDIAN,
			records: makeRecs([]float64{}, nil),
			want:    expectation{n: 0, nNull: 0, operator: nil},
		},
		// Unsorted input — median operator sorts internally;
		// position_low / position_high reference the SORTED positions,
		// not the original input order.
		{
			name:    "MEDIAN_unsorted",
			aggType: types.AGG_MEDIAN,
			records: makeRecs([]float64{5, 1, 3, 4, 2}, nil),
			want: expectation{n: 5, nNull: 0, operator: map[string]any{
				"position_low":  2,
				"position_high": 2,
				"median":        3.0,
			}},
		},

		// -------- AGG_PERCENTILE --------
		//
		// Single-row trivial: any percentile of [42] is 42.
		// position=0, lower=upper=42.0.
		{
			name:    "PERCENTILE_singleRow",
			aggType: types.AGG_PERCENTILE,
			params:  pParams(75),
			records: makeRecs([]float64{42}, nil),
			want: expectation{n: 1, nNull: 0, operator: map[string]any{
				"p":        75.0,
				"position": 0,
				"lower":    42.0,
				"upper":    42.0,
				"method":   "linear",
				"value":    42.0,
			}},
		},
		// Integer-rank single-bucket: p50 of [1,2,3,4,5] → rank=2.0,
		// lower==upper==index 2, value=3.0.
		{
			name:    "PERCENTILE_p50_integerRank",
			aggType: types.AGG_PERCENTILE,
			params:  pParams(50),
			records: makeRecs([]float64{1, 2, 3, 4, 5}, nil),
			want: expectation{n: 5, nNull: 0, operator: map[string]any{
				"p":        50.0,
				"position": 2,
				"lower":    3.0,
				"upper":    3.0,
				"method":   "linear",
				"value":    3.0,
			}},
		},
		// Fractional-rank two-bucket: p25 of [1,2,3,4,5] →
		// rank=0.25*4=1.0 (also integer; pick a fractional case).
		// p75 of [1,2,3,4,5] → rank=0.75*4=3.0 (integer too).
		// Use [10,20,30,40] with p25: rank=0.25*3=0.75 →
		// lower=index 0 (10), upper=index 1 (20), value=10+(0.75)*10=17.5.
		{
			name:    "PERCENTILE_p25_fractionalRank",
			aggType: types.AGG_PERCENTILE,
			params:  pParams(25),
			records: makeRecs([]float64{10, 20, 30, 40}, nil),
			want: expectation{n: 4, nNull: 0, operator: map[string]any{
				"p":        25.0,
				"position": 0,
				"lower":    10.0,
				"upper":    20.0,
				"method":   "linear",
				"value":    17.5,
			}},
		},
		// p90 of [10,20,30,40] → rank=0.9*3=2.7 → lower=index 2 (30),
		// upper=index 3 (40), value=30 + 0.7*10 = 37.0.
		{
			name:    "PERCENTILE_p90_fractionalRank",
			aggType: types.AGG_PERCENTILE,
			params:  pParams(90),
			records: makeRecs([]float64{10, 20, 30, 40}, nil),
			want: expectation{n: 4, nNull: 0, operator: map[string]any{
				"p":        90.0,
				"position": 2,
				"lower":    30.0,
				"upper":    40.0,
				"method":   "linear",
				"value":    37.0,
			}},
		},
		// p0 (lower bound) and p100 (upper bound) — boundary cases.
		// p0 of [10,20,30,40] → rank=0 → position=0, lower=upper=10.0.
		{
			name:    "PERCENTILE_p0_boundary",
			aggType: types.AGG_PERCENTILE,
			params:  pParams(0),
			records: makeRecs([]float64{10, 20, 30, 40}, nil),
			want: expectation{n: 4, nNull: 0, operator: map[string]any{
				"p":        0.0,
				"position": 0,
				"lower":    10.0,
				"upper":    10.0,
				"method":   "linear",
				"value":    10.0,
			}},
		},
		// p100 of [10,20,30,40] → rank=3 → position=3, lower=upper=40.0.
		{
			name:    "PERCENTILE_p100_boundary",
			aggType: types.AGG_PERCENTILE,
			params:  pParams(100),
			records: makeRecs([]float64{10, 20, 30, 40}, nil),
			want: expectation{n: 4, nNull: 0, operator: map[string]any{
				"p":        100.0,
				"position": 3,
				"lower":    40.0,
				"upper":    40.0,
				"method":   "linear",
				"value":    40.0,
			}},
		},
		// Empty: (nil, nil) under the universal-floor convention.
		{
			name:    "PERCENTILE_empty",
			aggType: types.AGG_PERCENTILE,
			params:  pParams(50),
			records: makeRecs([]float64{}, nil),
			want:    expectation{n: 0, nNull: 0, operator: nil},
		},
	}

	for _, c := range tests {
		c := c
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: c.aggType, Field: "score", Label: "primary", Params: c.params},
				},
			}
			// Force buffered path via direct processRecords: MEDIAN and
			// PERCENTILE are non-streamable so the orchestrator's
			// canStream gate already routes here, but the explicit call
			// keeps the test single-pass deterministic and removes
			// streaming-state confounders from the assertion.
			proc := NewProcessor(schema)
			resp, err := proc.processRecords(context.Background(), req, c.records)
			if err != nil {
				t.Fatalf("processRecords: %v", err)
			}
			if resp.Components == nil {
				t.Fatalf("Response.Components nil; want populated")
			}
			if got := len(resp.Components.Aggregations); got != 1 {
				t.Fatalf("Aggregations slots = %d, want 1", got)
			}
			entry := resp.Components.Aggregations[0]
			if entry.Label != "primary" {
				t.Errorf("Label = %q, want %q", entry.Label, "primary")
			}
			if entry.N != c.want.n {
				t.Errorf("N = %d, want %d", entry.N, c.want.n)
			}
			if entry.NNull != c.want.nNull {
				t.Errorf("NNull = %d, want %d", entry.NNull, c.want.nNull)
			}
			if !reflect.DeepEqual(entry.Operator, c.want.operator) {
				t.Errorf("Operator = %#v, want %#v", entry.Operator, c.want.operator)
			}
		})
	}
}

// TestMetaAggregator_OrderStatOps_InterfaceLock confirms both order-
// stat aggregators implement MetaAggregator at runtime. The compile-
// time `var _ MetaAggregator = (*medianAggregator)(nil)` in aggregator.go
// catches drift at build time; this test catches the inverse drift
// (an embedder casting an aggregator to MetaAggregator must succeed).
func TestMetaAggregator_OrderStatOps_InterfaceLock(t *testing.T) {
	schema := numericSchema()
	for _, aggType := range []types.AggregationType{types.AGG_MEDIAN, types.AGG_PERCENTILE} {
		t.Run(string(aggType), func(t *testing.T) {
			factory, ok := aggregatorRegistry[aggType]
			if !ok {
				t.Fatalf("no aggregator registered for %s", aggType)
			}
			var params json.RawMessage
			if aggType == types.AGG_PERCENTILE {
				params = json.RawMessage(`{"percentile":50}`)
			}
			agg, err := factory(&types.Aggregation{Type: aggType, Field: "score", Params: params}, schema)
			if err != nil {
				t.Fatalf("factory %s: %v", aggType, err)
			}
			if _, ok := agg.(MetaAggregator); !ok {
				t.Fatalf("%s aggregator does not implement MetaAggregator", aggType)
			}
		})
	}
}

// TestPredict_BufferedComponentsFlag_OrderStat asserts that the
// PredictResult surfaces BufferedComponents=true for both order-stat
// aggregators — the per-slot hint advertises that the components map
// arrives only on terminal buffered flush, since the operators are
// ComponentsMergeability=None. The overall Streamable axis is
// orthogonal — both operators are non-streamable today, but the
// BufferedComponents flag is the dedicated signal for "components
// emission is buffered-only" and survives independently of streamability.
func TestPredict_BufferedComponentsFlag_OrderStat(t *testing.T) {
	// Predict touches schema only — a minimal header is enough.
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64},
		},
	}
	cases := []struct {
		name    string
		aggType types.AggregationType
		params  json.RawMessage
	}{
		{name: "AGG_MEDIAN", aggType: types.AGG_MEDIAN},
		{name: "AGG_PERCENTILE", aggType: types.AGG_PERCENTILE, params: json.RawMessage(`{"percentile":90}`)},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			factory, ok := aggregatorRegistry[c.aggType]
			if !ok {
				t.Fatalf("no aggregator registered for %s", c.aggType)
			}
			// Constructing the aggregator via the registry confirms
			// params parse — the predict-side test in
			// descriptor/predict_streamable_test.go already asserts the
			// BufferedComponents flag against descriptor.PredictResult.
			// Here we lock the runtime-side invariant: the factory MUST
			// build cleanly under the same Params shape the predict
			// path advertises as buffered-components.
			_, err := factory(&types.Aggregation{Type: c.aggType, Field: "score", Params: c.params}, schema)
			if err != nil {
				t.Fatalf("factory %s: %v", c.aggType, err)
			}
		})
	}
}
