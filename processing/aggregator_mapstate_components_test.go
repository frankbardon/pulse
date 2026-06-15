package processing

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestMetaAggregator_MapStateOps_Components is the E1-S7 table-driven
// sweep covering Components() emission for the three map-state
// aggregators implemented in E1-S7: AGG_DISTINCT_COUNT / AGG_MODE /
// AGG_FREQUENCY. Each operator is exercised across four input shapes:
//
//   - empty: no input records.
//   - single-distinct: every row carries the same value.
//   - tie-mode: two values share the maximal count.
//   - full-unique: every row distinct (every value ties at count=1).
//
// The orchestrator runs the buffered processRecords exit so the
// frozen mirrors stamped by aggregateValues are the source of truth
// for Components(). Each case asserts (a) the universal floor (N,
// NNull) matches the per-record bookkeeping and (b) the operator-
// specific map matches the schema declared in
// descriptor/capabilities_aggregators.go byte-for-byte via
// reflect.DeepEqual.
func TestMetaAggregator_MapStateOps_Components(t *testing.T) {
	schema := numericSchema()
	makeRecs := func(values []float64) []*Record {
		return makeRecordsWithNulls(schema, "score", values, nil)
	}

	type expectation struct {
		n        int
		nNull    int
		operator map[string]any
	}
	type tc struct {
		name    string
		aggType types.AggregationType
		records []*Record
		want    expectation
	}

	tests := []tc{
		// -------- AGG_DISTINCT_COUNT --------
		//
		// Empty: orchestrator floor reports {n:0, n_null:0}; the
		// per-operator Components() returns the post-finalize
		// cardinality=0 so the consumer can read both channels
		// independently.
		{
			name:    "DISTINCT_COUNT_empty",
			aggType: types.AGG_DISTINCT_COUNT,
			records: makeRecs([]float64{}),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"cardinality": 0}},
		},
		{
			name:    "DISTINCT_COUNT_singleDistinct",
			aggType: types.AGG_DISTINCT_COUNT,
			records: makeRecs([]float64{7, 7, 7, 7}),
			want:    expectation{n: 4, nNull: 0, operator: map[string]any{"cardinality": 1}},
		},
		{
			name:    "DISTINCT_COUNT_tieMode",
			aggType: types.AGG_DISTINCT_COUNT,
			records: makeRecs([]float64{1, 1, 2, 2, 3}),
			want:    expectation{n: 5, nNull: 0, operator: map[string]any{"cardinality": 3}},
		},
		{
			name:    "DISTINCT_COUNT_fullUnique",
			aggType: types.AGG_DISTINCT_COUNT,
			records: makeRecs([]float64{10, 20, 30, 40, 50}),
			want:    expectation{n: 5, nNull: 0, operator: map[string]any{"cardinality": 5}},
		},

		// -------- AGG_MODE --------
		//
		// Empty: no defensible modal value — return (nil, nil) so
		// the universal floor's n=0 is the sole component channel.
		// Single-distinct: tie_count is 1 because exactly one value
		// shares the modal count. Tie-mode: tie_count is 2 because
		// two distinct values share the maximal count; value is the
		// smaller of the two (deterministic tie-break). Full-unique:
		// every value ties at count=1, so tie_count equals distinct_count.
		{
			name:    "MODE_empty",
			aggType: types.AGG_MODE,
			records: makeRecs([]float64{}),
			want:    expectation{n: 0, nNull: 0, operator: nil},
		},
		{
			name:    "MODE_singleDistinct",
			aggType: types.AGG_MODE,
			records: makeRecs([]float64{42, 42, 42}),
			want: expectation{n: 3, nNull: 0, operator: map[string]any{
				"value":          42.0,
				"count":          3,
				"distinct_count": 1,
				"tie_count":      1,
			}},
		},
		{
			name:    "MODE_tieMode",
			aggType: types.AGG_MODE,
			records: makeRecs([]float64{1, 1, 2, 2, 3}),
			want: expectation{n: 5, nNull: 0, operator: map[string]any{
				"value":          1.0, // smaller of {1, 2} — deterministic tie-break.
				"count":          2,
				"distinct_count": 3,
				"tie_count":      2,
			}},
		},
		{
			name:    "MODE_fullUnique",
			aggType: types.AGG_MODE,
			records: makeRecs([]float64{10, 20, 30, 40, 50}),
			want: expectation{n: 5, nNull: 0, operator: map[string]any{
				"value":          10.0, // smallest of the five tied-at-1 values.
				"count":          1,
				"distinct_count": 5,
				"tie_count":      5,
			}},
		},

		// -------- AGG_FREQUENCY --------
		//
		// Empty: no modal value — return (nil, nil) so the universal
		// floor's n=0 is the sole component channel. mode_value uses
		// the same smallest-value tie-break as AGG_MODE so the two
		// aggregators agree on the winner over the same input set.
		{
			name:    "FREQUENCY_empty",
			aggType: types.AGG_FREQUENCY,
			records: makeRecs([]float64{}),
			want:    expectation{n: 0, nNull: 0, operator: nil},
		},
		{
			name:    "FREQUENCY_singleDistinct",
			aggType: types.AGG_FREQUENCY,
			records: makeRecs([]float64{9, 9, 9, 9}),
			want: expectation{n: 4, nNull: 0, operator: map[string]any{
				"distinct_count": 1,
				"mode_value":     9.0,
				"mode_count":     4,
			}},
		},
		{
			name:    "FREQUENCY_tieMode",
			aggType: types.AGG_FREQUENCY,
			records: makeRecs([]float64{1, 1, 2, 2, 3}),
			want: expectation{n: 5, nNull: 0, operator: map[string]any{
				"distinct_count": 3,
				"mode_value":     1.0, // smaller of {1, 2}.
				"mode_count":     2,
			}},
		},
		{
			name:    "FREQUENCY_fullUnique",
			aggType: types.AGG_FREQUENCY,
			records: makeRecs([]float64{10, 20, 30, 40, 50}),
			want: expectation{n: 5, nNull: 0, operator: map[string]any{
				"distinct_count": 5,
				"mode_value":     10.0, // smallest of the five tied-at-1 values.
				"mode_count":     1,
			}},
		},
	}

	for _, c := range tests {
		c := c
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: c.aggType, Field: "score", Label: "primary"},
				},
			}
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

// TestMetaAggregator_MapStateOps_StreamingEmits exercises the
// streaming code path (processStreaming) for the three map-state
// aggregators and confirms the frozen mirrors stamped during Finalize
// produce a Components() emission byte-equal to the buffered path.
// The cohort uses fields with no two-pass attribute / grouper /
// feature so canStream() returns true.
func TestMetaAggregator_MapStateOps_StreamingEmits(t *testing.T) {
	schema := numericSchema()
	// Mix of distinct + duplicate + nulls. distinct_count=3, mode=10
	// (smallest among ties at count=2), modal count=2, n=5, n_null=2.
	records := makeRecordsWithNulls(schema, "score",
		[]float64{10, 0, 20, 0, 10, 20, 30}, []int{1, 3})

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_DISTINCT_COUNT, Field: "score", Label: "card"},
			{Type: types.AGG_MODE, Field: "score", Label: "mode"},
			{Type: types.AGG_FREQUENCY, Field: "score", Label: "freq"},
		},
	}
	proc := NewProcessor(schema)
	resp, err := proc.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if proc.LastPath() != PathStreaming {
		t.Fatalf("LastPath = %v, want PathStreaming", proc.LastPath())
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated")
	}
	if got := len(resp.Components.Aggregations); got != 3 {
		t.Fatalf("Aggregations slots = %d, want 3", got)
	}

	wantOps := []map[string]any{
		// AGG_DISTINCT_COUNT — three distinct values (10, 20, 30).
		{"cardinality": 3},
		// AGG_MODE — 10 and 20 tie at count=2; 10 wins on smallest-
		// value tie-break. tie_count = 2 (10 and 20).
		{"value": 10.0, "count": 2, "distinct_count": 3, "tie_count": 2},
		// AGG_FREQUENCY — same map; mode_value matches AGG_MODE's
		// winner; mode_count = 2; distinct_count = 3.
		{"distinct_count": 3, "mode_value": 10.0, "mode_count": 2},
	}
	wantLabels := []string{"card", "mode", "freq"}
	for i, entry := range resp.Components.Aggregations {
		if entry.Label != wantLabels[i] {
			t.Errorf("Label[%d] = %q, want %q", i, entry.Label, wantLabels[i])
		}
		// Three rows contributed (10, 20, 10, 20, 30 — n=5); two
		// nulls; the universal floor's n / n_null must agree across
		// every slot.
		if entry.N != 5 {
			t.Errorf("N[%d] = %d, want 5", i, entry.N)
		}
		if entry.NNull != 2 {
			t.Errorf("NNull[%d] = %d, want 2", i, entry.NNull)
		}
		if !reflect.DeepEqual(entry.Operator, wantOps[i]) {
			t.Errorf("Operator[%d] = %#v, want %#v", i, entry.Operator, wantOps[i])
		}
	}
}
