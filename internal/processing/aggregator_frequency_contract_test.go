package processing

import (
	"context"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// AGG_FREQUENCY's contract: ONE float64 per output row — the modal
// count (rows holding the most common value) — never a per-value map.
// Components add distinct_count, mode_value (smallest value wins a
// tie, as AGG_MODE) and mode_count. These tests pin the scalar, the
// tie-break, buffered == streaming, the exact MergeOnline fold, and
// the categorical smart-default pairing (GROUP_CATEGORY on X +
// AGG_FREQUENCY on X yields each group's row count).

// brand codes in categoricalSchema(): Apple=0, Samsung=1, Google=2.
func frequencyRecords(codes []float64) []*Record {
	return makeRecords(categoricalSchema(), "brand", codes)
}

func frequencyRequest(groups ...*types.Group) *types.Request {
	return &types.Request{
		Groups:       groups,
		Aggregations: []*types.Aggregation{{Type: types.AGG_FREQUENCY, Field: "brand", Label: "freq"}},
	}
}

func TestAggFrequency_ScalarIsModalCount_BufferedEqualsStreaming(t *testing.T) {
	cases := []struct {
		name      string
		codes     []float64
		wantCount float64
		wantOp    map[string]any
	}{
		// [a,a,b,c,c,c] — c holds 3 rows.
		{"modal count", []float64{0, 0, 1, 2, 2, 2}, 3,
			map[string]any{"distinct_count": 3, "mode_value": 2.0, "mode_count": 3}},
		// Google (2) is seen FIRST but Apple (0) is the smaller tied
		// value, so mode_value is 0 — smallest, not first-seen.
		{"tie keeps the count, smallest value wins", []float64{2, 2, 0, 0, 1}, 2,
			map[string]any{"distinct_count": 3, "mode_value": 0.0, "mode_count": 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := categoricalSchema()
			buffered, err := NewProcessor(schema).processRecords(context.Background(), frequencyRequest(), frequencyRecords(tc.codes))
			if err != nil {
				t.Fatalf("buffered: %v", err)
			}
			proc := NewProcessor(schema)
			streamed, err := proc.Process(context.Background(), frequencyRequest(), NewSliceIterator(frequencyRecords(tc.codes)))
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
				got, ok := resp.Data[0]["freq"].(float64)
				if !ok {
					t.Fatalf("%s: freq = %#v (%T), want a float64 scalar", name, resp.Data[0]["freq"], resp.Data[0]["freq"])
				}
				if got != tc.wantCount {
					t.Errorf("%s: freq = %v, want %v", name, got, tc.wantCount)
				}
				op := resp.Components.Aggregations[0].Operator
				if !reflect.DeepEqual(op, tc.wantOp) {
					t.Errorf("%s: components = %#v, want %#v", name, op, tc.wantOp)
				}
			}
		})
	}
}

// TestAggFrequency_GroupedBySameFieldIsRowCount: the categorical smart
// default pairs GROUP_CATEGORY and AGG_FREQUENCY on one field; each
// group then holds a single value, so the modal count is the group's
// row count.
func TestAggFrequency_GroupedBySameFieldIsRowCount(t *testing.T) {
	schema := categoricalSchema()
	resp, err := NewProcessor(schema).processRecords(context.Background(),
		frequencyRequest(&types.Group{Type: types.GROUP_CATEGORY, Field: "brand"}),
		frequencyRecords([]float64{0, 0, 1, 2, 2, 2}))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"Apple": 2, "Samsung": 1, "Google": 3}
	if len(resp.Data) != len(want) {
		t.Fatalf("%d rows, want %d: %v", len(resp.Data), len(want), resp.Data)
	}
	for _, row := range resp.Data {
		key, _ := row["brand"].(string)
		if row["freq"] != want[key] {
			t.Errorf("group %q: freq = %v, want %v", key, row["freq"], want[key])
		}
	}
}

// TestAggFrequency_MergeOnlineEqualsSerial: the online partials merge
// exactly — splitting the rows across two partials (as shard / segment
// workers and the chain's mergeable gate assume) yields the serial
// result, including a mode that only wins once the partials combine.
func TestAggFrequency_MergeOnlineEqualsSerial(t *testing.T) {
	schema := categoricalSchema()
	// Left alone the mode is Apple (2 of 3); right alone it is Samsung
	// (2 of 3); together Google wins 3-of-6 with 1+2 split across both.
	left := frequencyRecords([]float64{0, 0, 2})
	right := frequencyRecords([]float64{1, 1, 2, 2})
	all := append(append([]*Record{}, left...), right...)

	serial := makeAggregator(t, types.AGG_FREQUENCY, "brand", schema)
	wantScalar, err := serial.Aggregate(all, "brand")
	if err != nil {
		t.Fatal(err)
	}
	wantOp, _ := serial.(MetaAggregator).Components()

	partial := func(recs []*Record) MergeableAggregator {
		a := makeAggregator(t, types.AGG_FREQUENCY, "brand", schema).(MergeableAggregator)
		for _, r := range recs {
			if err := a.UpdateRow(r, "brand"); err != nil {
				t.Fatal(err)
			}
		}
		return a
	}
	a, b := partial(left), partial(right)
	if err := a.MergeOnline(b); err != nil {
		t.Fatal(err)
	}
	got, err := a.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if got != wantScalar || got != 3 {
		t.Errorf("merged = %v, serial = %v, want 3", got, wantScalar)
	}
	gotOp, _ := a.(MetaAggregator).Components()
	if !reflect.DeepEqual(gotOp, wantOp) {
		t.Errorf("merged components = %#v, serial = %#v", gotOp, wantOp)
	}
}
