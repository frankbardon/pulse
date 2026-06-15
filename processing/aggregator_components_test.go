package processing

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestMetaAggregator_ScalarOps_Components is the table-driven sweep
// covering Components() emission for the seven scalar aggregators
// implemented in E1-S5: AGG_COUNT / AGG_SUM / AGG_AVERAGE / AGG_MIN /
// AGG_MAX / AGG_RANGE / AGG_NULL_COUNT. Each operator is exercised
// across three input shapes:
//
//   - populated: 3-5 non-null rows.
//   - empty: no input records at all (filtered out upstream).
//   - null-heavy: every input row is null for the target field.
//
// The orchestrator runs the buffered processRecords exit (no streaming
// gates here on a small in-memory cohort) so Response.Components is
// populated through the same buildAggregationComponents helper the
// streaming exits use. Each case asserts (a) the universal floor
// (N, NNull) matches the per-record bookkeeping, (b) the operator-
// specific map matches the schema-declared key set byte-for-byte via
// reflect.DeepEqual.
func TestMetaAggregator_ScalarOps_Components(t *testing.T) {
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
		records []*Record
		want    expectation
	}

	populated := []float64{10, 20, 30}
	empty := []float64{}
	nullHeavy := []float64{0, 0, 0}
	nullHeavyIdx := []int{0, 1, 2}

	tests := []tc{
		// AGG_COUNT — floor only.
		{
			name:    "COUNT_populated",
			aggType: types.AGG_COUNT,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: nil},
		},
		{
			name:    "COUNT_empty",
			aggType: types.AGG_COUNT,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: nil},
		},
		{
			name:    "COUNT_nullHeavy",
			aggType: types.AGG_COUNT,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: nil},
		},

		// AGG_SUM — {sum}.
		{
			name:    "SUM_populated",
			aggType: types.AGG_SUM,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"sum": 60.0}},
		},
		{
			name:    "SUM_empty",
			aggType: types.AGG_SUM,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"sum": 0.0}},
		},
		{
			name:    "SUM_nullHeavy",
			aggType: types.AGG_SUM,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"sum": 0.0}},
		},

		// AGG_AVERAGE — {sum} only (mean derivable as sum / n).
		{
			name:    "AVERAGE_populated",
			aggType: types.AGG_AVERAGE,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"sum": 60.0}},
		},
		{
			name:    "AVERAGE_empty",
			aggType: types.AGG_AVERAGE,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"sum": 0.0}},
		},
		{
			name:    "AVERAGE_nullHeavy",
			aggType: types.AGG_AVERAGE,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"sum": 0.0}},
		},

		// AGG_MIN — {min}.
		{
			name:    "MIN_populated",
			aggType: types.AGG_MIN,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"min": 10.0}},
		},
		{
			name:    "MIN_empty",
			aggType: types.AGG_MIN,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"min": 0.0}},
		},
		{
			name:    "MIN_nullHeavy",
			aggType: types.AGG_MIN,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"min": 0.0}},
		},

		// AGG_MAX — {max}.
		{
			name:    "MAX_populated",
			aggType: types.AGG_MAX,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"max": 30.0}},
		},
		{
			name:    "MAX_empty",
			aggType: types.AGG_MAX,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"max": 0.0}},
		},
		{
			name:    "MAX_nullHeavy",
			aggType: types.AGG_MAX,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"max": 0.0}},
		},

		// AGG_RANGE — {min, max}.
		{
			name:    "RANGE_populated",
			aggType: types.AGG_RANGE,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: map[string]any{"min": 10.0, "max": 30.0}},
		},
		{
			name:    "RANGE_empty",
			aggType: types.AGG_RANGE,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: map[string]any{"min": 0.0, "max": 0.0}},
		},
		{
			name:    "RANGE_nullHeavy",
			aggType: types.AGG_RANGE,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: map[string]any{"min": 0.0, "max": 0.0}},
		},

		// AGG_NULL_COUNT — floor only.
		{
			name:    "NULL_COUNT_populated",
			aggType: types.AGG_NULL_COUNT,
			records: makeRecs(populated, nil),
			want:    expectation{n: 3, nNull: 0, operator: nil},
		},
		{
			name:    "NULL_COUNT_empty",
			aggType: types.AGG_NULL_COUNT,
			records: makeRecs(empty, nil),
			want:    expectation{n: 0, nNull: 0, operator: nil},
		},
		{
			name:    "NULL_COUNT_nullHeavy",
			aggType: types.AGG_NULL_COUNT,
			records: makeRecs(nullHeavy, nullHeavyIdx),
			want:    expectation{n: 0, nNull: 3, operator: nil},
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
			// Force buffered path via direct processRecords so the test
			// is single-pass deterministic; the streaming exit shares
			// the same buildAggregationComponents emit helper and is
			// covered separately by TestMetaAggregator_StreamingEmits.
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

// TestMetaAggregator_StreamingEmits exercises the streaming-path exit
// (processStreaming) and confirms that the same per-slot
// AggregationComponents entry is emitted byte-equal to the buffered
// path. The cohort uses fields with no two-pass attribute / grouper /
// feature so canStream() returns true.
func TestMetaAggregator_StreamingEmits(t *testing.T) {
	schema := numericSchema()
	records := makeRecordsWithNulls(schema, "score",
		[]float64{10, 0, 30, 0, 50}, []int{1, 3})

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
			{Type: types.AGG_MIN, Field: "score", Label: "lo"},
			{Type: types.AGG_MAX, Field: "score", Label: "hi"},
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
		{"sum": 90.0},
		{"min": 10.0},
		{"max": 50.0},
	}
	wantLabels := []string{"total", "lo", "hi"}
	for i, entry := range resp.Components.Aggregations {
		if entry.Label != wantLabels[i] {
			t.Errorf("Label[%d] = %q, want %q", i, entry.Label, wantLabels[i])
		}
		if entry.N != 3 {
			t.Errorf("N[%d] = %d, want 3", i, entry.N)
		}
		if entry.NNull != 2 {
			t.Errorf("NNull[%d] = %d, want 2", i, entry.NNull)
		}
		if !reflect.DeepEqual(entry.Operator, wantOps[i]) {
			t.Errorf("Operator[%d] = %#v, want %#v", i, entry.Operator, wantOps[i])
		}
	}
}

// TestComponentsUniversalFloor is the E1-S5 stub of the universal-
// floor invariant gate: for each of the seven scalar AGGs implemented
// here, assert that the orchestrator-emitted floor is non-negative and
// that N + NNull never exceeds the post-filter record count. The full
// sweep across every registered operator lands in E1-S12; this stub
// keeps the contract gateable from day one of the components rollout.
func TestComponentsUniversalFloor(t *testing.T) {
	schema := numericSchema()
	records := makeRecordsWithNulls(schema, "score",
		[]float64{10, 0, 30, 0, 50}, []int{1, 3})
	const filteredRecords = 5

	scalarOps := []types.AggregationType{
		types.AGG_COUNT,
		types.AGG_SUM,
		types.AGG_AVERAGE,
		types.AGG_MIN,
		types.AGG_MAX,
		types.AGG_RANGE,
		types.AGG_NULL_COUNT,
	}
	for _, op := range scalarOps {
		op := op
		t.Run(string(op), func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: op, Field: "score", Label: "x"},
				},
			}
			proc := NewProcessor(schema)
			resp, err := proc.processRecords(context.Background(), req, records)
			if err != nil {
				t.Fatalf("processRecords: %v", err)
			}
			if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
				t.Fatalf("Components shape wrong: %+v", resp.Components)
			}
			entry := resp.Components.Aggregations[0]
			if entry.N < 0 {
				t.Errorf("%s: N = %d, want >= 0", op, entry.N)
			}
			if entry.NNull < 0 {
				t.Errorf("%s: NNull = %d, want >= 0", op, entry.NNull)
			}
			if entry.N+entry.NNull > filteredRecords {
				t.Errorf("%s: N (%d) + NNull (%d) = %d, want <= %d",
					op, entry.N, entry.NNull, entry.N+entry.NNull, filteredRecords)
			}
		})
	}
}
