package processing

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestMetaAggregator_CompositeOps_Components(t *testing.T) {
	schema := cohortSchema()

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

	// AGG_WEIGHTED_MEAN: values * weights = sum_weighted; Σ weights =
	// sum_weights; ratio = weighted_mean. The cohort_schema's `value`
	// is the field; `weight` is the weight_field.
	wmParams := json.RawMessage(`{"weight_field":"weight"}`)
	// AGG_RATIO: num + den summed independently.
	ratioParams := json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`)

	tests := []tc{
		// -------- AGG_WEIGHTED_MEAN --------
		//
		// Normal positive-weight inputs. Values (10, 20, 30) weighted
		// (1, 1, 2): sum_weighted = 10*1 + 20*1 + 30*2 = 90;
		// sum_weights = 4; weighted_mean = 22.5.
		{
			name:    "WEIGHTED_MEAN_positiveWeights",
			aggType: types.AGG_WEIGHTED_MEAN,
			params:  wmParams,
			records: []*Record{
				cohortRec(10, 1, 0, 0, nil),
				cohortRec(20, 1, 0, 0, nil),
				cohortRec(30, 2, 0, 0, nil),
			},
			want: expectation{n: 3, nNull: 0, operator: map[string]any{
				"sum_weighted":  90.0,
				"sum_weights":   4.0,
				"weighted_mean": 22.5,
			}},
		},
		// Zero-weight rows: every contributing row has weight==0.
		// Scalar return falls back to 0; components reflect raw state
		// — every accumulator is 0. Universal floor n records the
		// per-record bookkeeping (the orchestrator still saw three
		// non-null `value` rows; weight==0 is an aggregator-internal
		// skip), so n=3, n_null=0.
		{
			name:    "WEIGHTED_MEAN_zeroWeightRows",
			aggType: types.AGG_WEIGHTED_MEAN,
			params:  wmParams,
			records: []*Record{
				cohortRec(10, 0, 0, 0, nil),
				cohortRec(20, 0, 0, 0, nil),
				cohortRec(30, 0, 0, 0, nil),
			},
			want: expectation{n: 3, nNull: 0, operator: map[string]any{
				"sum_weighted":  0.0,
				"sum_weights":   0.0,
				"weighted_mean": 0.0,
			}},
		},
		// Empty input: no records contributed. Frozen-mirror stamps
		// via the empty-fold path; components surface zeros.
		{
			name:    "WEIGHTED_MEAN_empty",
			aggType: types.AGG_WEIGHTED_MEAN,
			params:  wmParams,
			records: []*Record{},
			want: expectation{n: 0, nNull: 0, operator: map[string]any{
				"sum_weighted":  0.0,
				"sum_weights":   0.0,
				"weighted_mean": 0.0,
			}},
		},
		// Single-row input. value=42, weight=2 → sum_weighted=84,
		// sum_weights=2, weighted_mean=42.
		{
			name:    "WEIGHTED_MEAN_singleRow",
			aggType: types.AGG_WEIGHTED_MEAN,
			params:  wmParams,
			records: []*Record{
				cohortRec(42, 2, 0, 0, nil),
			},
			want: expectation{n: 1, nNull: 0, operator: map[string]any{
				"sum_weighted":  84.0,
				"sum_weights":   2.0,
				"weighted_mean": 42.0,
			}},
		},

		// -------- AGG_RATIO --------
		//
		// Normal numerator+denominator. num=(10, 30) den=(2, 8) →
		// numerator=40, denominator=10, ratio=4.0. The aggregation's
		// own Field is `value` (ignored by AGG_RATIO); the universal
		// floor's n / n_null still reflects per-record bookkeeping
		// against `value`.
		{
			name:    "RATIO_positiveDenominator",
			aggType: types.AGG_RATIO,
			params:  ratioParams,
			records: []*Record{
				cohortRec(0, 0, 10, 2, nil),
				cohortRec(0, 0, 30, 8, nil),
			},
			want: expectation{n: 2, nNull: 0, operator: map[string]any{
				"numerator":   40.0,
				"denominator": 10.0,
				"ratio":       4.0,
			}},
		},
		// Zero-denominator: every row's den==0 → numerator>0,
		// denominator==0, ratio=NaN. The frozen mirror preserves
		// NaN byte-for-byte against the scalar return.
		{
			name:    "RATIO_zeroDenominator",
			aggType: types.AGG_RATIO,
			params:  ratioParams,
			records: []*Record{
				cohortRec(0, 0, 5, 0, nil),
				cohortRec(0, 0, 7, 0, nil),
			},
			want: expectation{n: 2, nNull: 0, operator: map[string]any{
				"numerator":   12.0,
				"denominator": 0.0,
				"ratio":       math.NaN(),
			}},
		},
		// Empty input: no records contributed. Frozen-mirror stamps
		// from the empty fold; denominator==0 → ratio=NaN.
		{
			name:    "RATIO_empty",
			aggType: types.AGG_RATIO,
			params:  ratioParams,
			records: []*Record{},
			want: expectation{n: 0, nNull: 0, operator: map[string]any{
				"numerator":   0.0,
				"denominator": 0.0,
				"ratio":       math.NaN(),
			}},
		},
		// Single-row input. num=5, den=2 → ratio=2.5.
		{
			name:    "RATIO_singleRow",
			aggType: types.AGG_RATIO,
			params:  ratioParams,
			records: []*Record{
				cohortRec(0, 0, 5, 2, nil),
			},
			want: expectation{n: 1, nNull: 0, operator: map[string]any{
				"numerator":   5.0,
				"denominator": 2.0,
				"ratio":       2.5,
			}},
		},
	}

	for _, c := range tests {
		c := c
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: c.aggType, Field: "value", Label: "primary", Params: c.params},
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
			if !componentsEqual(entry.Operator, c.want.operator) {
				t.Errorf("Operator = %#v, want %#v", entry.Operator, c.want.operator)
			}
		})
	}
}

// componentsEqual compares two component maps allowing NaN==NaN under
// the IEEE-754 inequality contract — reflect.DeepEqual treats NaN as
// non-equal even against itself, so the parity tests need a custom
// comparator that treats two NaNs in the same slot as equal. Non-NaN
// float64 entries are compared bit-equal via reflect.DeepEqual against
// the per-key value to preserve the existing strict-equality contract.
func componentsEqual(got, want map[string]any) bool {
	if len(got) != len(want) {
		return false
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			return false
		}
		gf, gOk := g.(float64)
		wf, wOk := w.(float64)
		if gOk && wOk && math.IsNaN(gf) && math.IsNaN(wf) {
			continue
		}
		if !reflect.DeepEqual(g, w) {
			return false
		}
	}
	return true
}

// TestMetaAggregator_CompositeOps_StreamingEmits exercises the
// streaming code path (processStreaming) for AGG_WEIGHTED_MEAN +
// AGG_RATIO and confirms the frozen mirrors stamped during Finalize
// produce a Components() emission byte-equal to the buffered path.
// The cohort uses fields with no two-pass attribute / grouper /
// feature so canStream() returns true.
func TestMetaAggregator_CompositeOps_StreamingEmits(t *testing.T) {
	schema := cohortSchema()

	// Mixed inputs: weights 1, 1, 2 + nums 4, 6, 0 + dens 2, 3, 5.
	// weighted_mean over (10, 20, 30) with weights (1, 1, 2):
	//   sum_weighted = 10 + 20 + 60 = 90; sum_weights = 4 → 22.5.
	// ratio: numerator = 10, denominator = 10 → 1.0.
	records := []*Record{
		cohortRec(10, 1, 4, 2, nil),
		cohortRec(20, 1, 6, 3, nil),
		cohortRec(30, 2, 0, 5, nil),
	}

	req := &types.Request{
		Aggregations: []*types.Aggregation{
			{
				Type:   types.AGG_WEIGHTED_MEAN,
				Field:  "value",
				Label:  "wm",
				Params: json.RawMessage(`{"weight_field":"weight"}`),
			},
			{
				Type:   types.AGG_RATIO,
				Field:  "value",
				Label:  "r",
				Params: json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`),
			},
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
	if got := len(resp.Components.Aggregations); got != 2 {
		t.Fatalf("Aggregations slots = %d, want 2", got)
	}

	wantOps := []map[string]any{
		// AGG_WEIGHTED_MEAN — sum_weighted (10 + 20 + 60), sum_weights
		// (4), weighted_mean (22.5).
		{
			"sum_weighted":  90.0,
			"sum_weights":   4.0,
			"weighted_mean": 22.5,
		},
		// AGG_RATIO — numerator (10), denominator (10), ratio (1.0).
		{
			"numerator":   10.0,
			"denominator": 10.0,
			"ratio":       1.0,
		},
	}
	wantLabels := []string{"wm", "r"}
	for i, entry := range resp.Components.Aggregations {
		if entry.Label != wantLabels[i] {
			t.Errorf("Label[%d] = %q, want %q", i, entry.Label, wantLabels[i])
		}
		// Three records contributed (no null `value` fields), so the
		// universal floor's n=3, n_null=0 across every slot.
		if entry.N != 3 {
			t.Errorf("N[%d] = %d, want 3", i, entry.N)
		}
		if entry.NNull != 0 {
			t.Errorf("NNull[%d] = %d, want 0", i, entry.NNull)
		}
		if !componentsEqual(entry.Operator, wantOps[i]) {
			t.Errorf("Operator[%d] = %#v, want %#v", i, entry.Operator, wantOps[i])
		}
	}
}

// TestMetaAggregator_CompositeOps_BufferedStreamingParity asserts that
// the buffered processRecords exit and the streaming Process exit
// produce byte-equal Components() maps for the composite aggregators
// over the same input. The frozen mirrors are stamped via different
// call sites (Aggregate's freeze vs Finalize's freeze) but must
// converge on identical post-result state — any divergence is a
// regression in the freeze contract.
func TestMetaAggregator_CompositeOps_BufferedStreamingParity(t *testing.T) {
	schema := cohortSchema()
	records := []*Record{
		cohortRec(1.5, 0.5, 7, 3, nil),
		cohortRec(4.0, 2.0, 11, 5, nil),
		cohortRec(7.0, 1.5, 2, 2, nil),
		cohortRec(9.0, 0.5, 5, 1, nil),
	}

	cases := []struct {
		name    string
		aggType types.AggregationType
		params  json.RawMessage
	}{
		{
			name:    "WEIGHTED_MEAN",
			aggType: types.AGG_WEIGHTED_MEAN,
			params:  json.RawMessage(`{"weight_field":"weight"}`),
		},
		{
			name:    "RATIO",
			aggType: types.AGG_RATIO,
			params:  json.RawMessage(`{"numerator_field":"num","denominator_field":"den"}`),
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Aggregations: []*types.Aggregation{
					{Type: c.aggType, Field: "value", Label: "primary", Params: c.params},
				},
			}
			// Buffered path.
			buf := NewProcessor(schema)
			bufResp, err := buf.processRecords(context.Background(), req, records)
			if err != nil {
				t.Fatalf("processRecords: %v", err)
			}
			// Streaming path — fresh processor so the in-memory
			// iterator drives processStreaming end-to-end.
			str := NewProcessor(schema)
			strResp, err := str.Process(context.Background(), req, NewSliceIterator(records))
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if str.LastPath() != PathStreaming {
				t.Fatalf("streaming LastPath = %v, want PathStreaming", str.LastPath())
			}
			if bufResp.Components == nil || strResp.Components == nil {
				t.Fatalf("Components nil: buf=%v str=%v",
					bufResp.Components, strResp.Components)
			}
			if len(bufResp.Components.Aggregations) != 1 ||
				len(strResp.Components.Aggregations) != 1 {
				t.Fatalf("slot count mismatch: buf=%d str=%d",
					len(bufResp.Components.Aggregations),
					len(strResp.Components.Aggregations))
			}
			bufEntry := bufResp.Components.Aggregations[0]
			strEntry := strResp.Components.Aggregations[0]
			if bufEntry.N != strEntry.N {
				t.Errorf("N: buf=%d str=%d", bufEntry.N, strEntry.N)
			}
			if bufEntry.NNull != strEntry.NNull {
				t.Errorf("NNull: buf=%d str=%d", bufEntry.NNull, strEntry.NNull)
			}
			if !componentsEqual(bufEntry.Operator, strEntry.Operator) {
				t.Errorf("Operator mismatch:\n buf=%#v\n str=%#v",
					bufEntry.Operator, strEntry.Operator)
			}
		})
	}
}
