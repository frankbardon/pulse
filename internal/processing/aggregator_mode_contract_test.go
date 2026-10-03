package processing

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// AGG_MODE's contract: ONE float64 per output row — the modal value
// itself (for a categorical field, its dictionary index), never a
// string — and a tie goes to the SMALLEST tied value, not the first one
// seen. The tie case below puts the larger tied value first so a
// first-seen tie-break would pick it; buffered and streaming must agree.

func modeRequest() *types.Request {
	return &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_MODE, Field: "brand", Label: "mode"}},
	}
}

func TestAggMode_ScalarIsSmallestTiedValue_BufferedEqualsStreaming(t *testing.T) {
	cases := []struct {
		name      string
		codes     []float64
		wantValue float64
		wantOp    map[string]any
	}{
		// [a,a,b,c,c,c] — c (index 2) is the mode.
		{"clear mode", []float64{0, 0, 1, 2, 2, 2}, 2,
			map[string]any{"value": 2.0, "count": 3, "distinct_count": 3, "tie_count": 1}},
		// Google (2) is seen FIRST but Apple (0) is the smaller tied
		// value, so the mode is 0 — smallest, not first-seen.
		{"tie goes to the smallest value", []float64{2, 2, 0, 0, 1}, 0,
			map[string]any{"value": 0.0, "count": 2, "distinct_count": 3, "tie_count": 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := categoricalSchema()
			recs := func() []*Record { return makeRecords(schema, "brand", tc.codes) }
			buffered, err := NewProcessor(schema).processRecords(context.Background(), modeRequest(), recs())
			if err != nil {
				t.Fatalf("buffered: %v", err)
			}
			proc := NewProcessor(schema)
			streamed, err := proc.Process(context.Background(), modeRequest(), NewSliceIterator(recs()))
			if err != nil {
				t.Fatalf("streaming: %v", err)
			}
			if proc.LastPath() != PathStreaming {
				t.Fatalf("LastPath = %v, want PathStreaming", proc.LastPath())
			}
			for name, resp := range map[string]*types.Response{"buffered": buffered, "streaming": streamed} {
				if len(resp.Data) != 1 {
					t.Fatalf("%s: %d rows, want 1", name, len(resp.Data))
				}
				got, ok := resp.Data[0]["mode"].(float64)
				if !ok {
					t.Fatalf("%s: mode = %#v (%T), want a float64 scalar", name, resp.Data[0]["mode"], resp.Data[0]["mode"])
				}
				if got != tc.wantValue {
					t.Errorf("%s: mode = %v, want %v", name, got, tc.wantValue)
				}
				op := resp.Components.Aggregations[0].Operator
				if !reflect.DeepEqual(op, tc.wantOp) {
					t.Errorf("%s: components = %#v, want %#v", name, op, tc.wantOp)
				}
			}
		})
	}
}
