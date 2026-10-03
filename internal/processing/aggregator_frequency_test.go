package processing

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// AGG_FREQUENCY's contract: ONE float64 per output row — the number of
// non-null rows whose field equals params.value — matched by the
// FILTER_INCLUDE rule (category label, else a number). Components add
// match_count (= the scalar) and share (match_count / n, omitted at
// n = 0). These tests pin buffered == streaming == merged on the three
// field families the rule distinguishes, the grouped form, zero-match,
// null handling, and both build-time refusals.

func frequencyAgg(field, params string) *types.Aggregation {
	a := &types.Aggregation{Type: types.AGG_FREQUENCY, Field: field, Label: "hits"}
	if params != "" {
		a.Params = json.RawMessage(params)
	}
	return a
}

func buildFrequency(t *testing.T, schema *encoding.Schema, field, params string) Aggregator {
	t.Helper()
	agg, err := newFrequencyAggregator(frequencyAgg(field, params), schema)
	if err != nil {
		t.Fatalf("newFrequencyAggregator(%s): %v", params, err)
	}
	return agg
}

func TestAggFrequency_BufferedStreamingMergedAgree(t *testing.T) {
	cat := categoricalSchema() // brand: Apple=0, Samsung=1, Google=2
	num := numericSchema()
	boolS := boolSchema()

	cases := []struct {
		name    string
		schema  *encoding.Schema
		field   string
		params  string
		values  []float64
		nulls   []int
		want    float64
		wantOps map[string]any
	}{
		// Samsung by LABEL; the two null rows hold Samsung's code but
		// are not counted and are not in share's base.
		{"categorical label", cat, "brand", `{"value":"Samsung"}`,
			[]float64{1, 0, 1, 2, 1, 1, 1}, []int{5, 6}, 3,
			map[string]any{"match_count": 3, "share": 0.6}},
		{"numeric string", num, "score", `{"value":"2.5"}`,
			[]float64{2.5, 1, 2.5, 4, 2.5, 2.5}, []int{0}, 3,
			map[string]any{"match_count": 3, "share": 0.6}},
		{"numeric JSON number", num, "score", `{"value":2.5}`,
			[]float64{2.5, 1, 2.5, 4}, nil, 2,
			map[string]any{"match_count": 2, "share": 0.5}},
		{"packed_bool true", boolS, "flag", `{"value":"1"}`,
			[]float64{1, 0, 1, 1, 0}, []int{3}, 2,
			map[string]any{"match_count": 2, "share": 0.5}},
		{"label no dictionary entry holds", cat, "brand", `{"value":"Nokia"}`,
			[]float64{0, 1, 2}, nil, 0,
			map[string]any{"match_count": 0, "share": 0.0}},
		{"number no row holds", num, "score", `{"value":"99"}`,
			[]float64{1, 2}, nil, 0,
			map[string]any{"match_count": 0, "share": 0.0}},
		{"every row null", num, "score", `{"value":"1"}`,
			[]float64{1, 1}, []int{0, 1}, 0,
			map[string]any{"match_count": 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recs := makeRecordsWithNulls(tc.schema, tc.field, tc.values, tc.nulls)

			buffered := buildFrequency(t, tc.schema, tc.field, tc.params)
			gotB, err := buffered.Aggregate(recs, tc.field)
			if err != nil {
				t.Fatal(err)
			}
			opsB, _ := buffered.(MetaAggregator).Components()

			streamed := buildFrequency(t, tc.schema, tc.field, tc.params).(MergeableAggregator)
			for _, r := range recs {
				if err := streamed.UpdateRow(r, tc.field); err != nil {
					t.Fatal(err)
				}
			}
			gotS, _ := streamed.Finalize()
			opsS, _ := streamed.(MetaAggregator).Components()

			// Merged: split the rows across two partials (shard /
			// segment workers) and fold.
			mid := len(recs) / 2
			left := buildFrequency(t, tc.schema, tc.field, tc.params).(MergeableAggregator)
			right := buildFrequency(t, tc.schema, tc.field, tc.params).(MergeableAggregator)
			for _, r := range recs[:mid] {
				_ = left.UpdateRow(r, tc.field)
			}
			for _, r := range recs[mid:] {
				_ = right.UpdateRow(r, tc.field)
			}
			if err := left.MergeOnline(right); err != nil {
				t.Fatal(err)
			}
			gotM, _ := left.Finalize()
			opsM, _ := left.(MetaAggregator).Components()

			for name, got := range map[string]float64{"buffered": gotB, "streaming": gotS, "merged": gotM} {
				if got != tc.want {
					t.Errorf("%s = %v, want %v", name, got, tc.want)
				}
			}
			for name, ops := range map[string]map[string]any{"buffered": opsB, "streaming": opsS, "merged": opsM} {
				if !reflect.DeepEqual(ops, tc.wantOps) {
					t.Errorf("%s components = %#v, want %#v", name, ops, tc.wantOps)
				}
			}
		})
	}
}

// TestAggFrequency_MergeSumsBothPartials: a match on each side of the
// split — a merge that kept either partial alone reads 1, not 2.
func TestAggFrequency_MergeSumsBothPartials(t *testing.T) {
	schema := numericSchema()
	a := buildFrequency(t, schema, "score", `{"value":"7"}`).(MergeableAggregator)
	b := buildFrequency(t, schema, "score", `{"value":"7"}`).(MergeableAggregator)
	for _, r := range makeRecords(schema, "score", []float64{7, 1, 1}) {
		_ = a.UpdateRow(r, "score")
	}
	for _, r := range makeRecords(schema, "score", []float64{7}) {
		_ = b.UpdateRow(r, "score")
	}
	if err := a.MergeOnline(b); err != nil {
		t.Fatal(err)
	}
	got, _ := a.Finalize()
	ops, _ := a.(MetaAggregator).Components()
	if got != 2 || !reflect.DeepEqual(ops, map[string]any{"match_count": 2, "share": 0.5}) {
		t.Errorf("merged = %v %#v, want 2 {match_count 2, share 0.5}", got, ops)
	}
}

// TestAggFrequency_GroupedBufferedEqualsStreaming runs the processor
// end to end under GROUP_CATEGORY on another field: one count per
// group, a group with no match reads 0, and both paths agree.
func TestAggFrequency_GroupedBufferedEqualsStreaming(t *testing.T) {
	schema := categoricalSchema()
	rows := []struct {
		brand, score float64
	}{
		{1, 1}, {1, 1}, {0, 1}, // score 1: two Samsung
		{0, 2}, {2, 2}, // score 2: none
		{1, 3}, // score 3: one Samsung
	}
	mk := func() []*Record {
		out := make([]*Record, len(rows))
		for i, r := range rows {
			out[i] = NewRecord(schema, map[string]float64{"brand": r.brand, "score": r.score})
		}
		return out
	}
	req := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "score"}},
		Aggregations: []*types.Aggregation{frequencyAgg("brand", `{"value":"Samsung"}`)},
		Sort:         []types.OrderKey{{Field: "score"}},
	}
	buffered, err := NewProcessor(schema).processRecords(context.Background(), req, mk())
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}
	proc := NewProcessor(schema)
	streamed, err := proc.Process(context.Background(), req, NewSliceIterator(mk()))
	if err != nil {
		t.Fatalf("streaming: %v", err)
	}
	if proc.LastPath() != PathStreaming {
		t.Fatalf("LastPath = %v, want PathStreaming", proc.LastPath())
	}
	want := []float64{2, 0, 1}
	for name, resp := range map[string]*types.Response{"buffered": buffered, "streaming": streamed} {
		if len(resp.Data) != len(want) {
			t.Fatalf("%s: %d rows, want %d: %v", name, len(resp.Data), len(want), resp.Data)
		}
		for i, row := range resp.Data {
			if row["hits"] != want[i] {
				t.Errorf("%s row %d (%v): hits = %v, want %v", name, i, row["score"], row["hits"], want[i])
			}
		}
	}
	if !reflect.DeepEqual(buffered.Data, streamed.Data) {
		t.Errorf("buffered %v != streaming %v", buffered.Data, streamed.Data)
	}
}

// TestAggFrequency_MissingValueRefused: absent, null, empty, and
// non-scalar values are refused at construction with PROCESSING_CONFIG
// naming params.value and pointing at AGG_MODE_COUNT.
func TestAggFrequency_MissingValueRefused(t *testing.T) {
	for _, params := range []string{"", `{}`, `{"value":null}`, `{"value":""}`, `{"value":true}`, `{"value":["a"]}`} {
		_, err := newFrequencyAggregator(frequencyAgg("score", params), numericSchema())
		if err == nil || !errors.HasCode(err, errors.PROCESSING_CONFIG) {
			t.Fatalf("params %q: err = %v, want PROCESSING_CONFIG", params, err)
		}
		for _, want := range []string{"params.value", "AGG_MODE_COUNT"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("params %q: message %q lacks %q", params, err.Error(), want)
			}
		}
	}
	// A hidden AGG_MODE_COUNT drops the clause on the way out.
	_, err := newFrequencyAggregator(frequencyAgg("score", ""), numericSchema())
	scoped := scopedRegistry("AGG_MODE_COUNT").ScopeRefusal(err)
	if strings.Contains(scoped.Error(), "AGG_MODE_COUNT") || !strings.Contains(scoped.Error(), "params.value") {
		t.Errorf("scoped refusal = %q, want params.value without AGG_MODE_COUNT", scoped.Error())
	}
}

// TestAggFrequency_UnparseableNumberRefused: a non-numeric value on a
// numeric field is refused like FILTER_INCLUDE refuses it.
func TestAggFrequency_UnparseableNumberRefused(t *testing.T) {
	_, err := newFrequencyAggregator(frequencyAgg("score", `{"value":"high"}`), numericSchema())
	if err == nil || !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Fatalf("err = %v, want PROCESSING_CONFIG", err)
	}
}

// TestAggFrequency_SetFieldRefused: a set column is refused at build
// time, pointing at AGG_SET_FREQUENCY.
func TestAggFrequency_SetFieldRefused(t *testing.T) {
	schema := makeSetTestSchema(t)
	_, err := newFrequencyAggregator(frequencyAgg("tags", `{"value":"VISA"}`), schema)
	if err == nil || !errors.HasCode(err, errors.PROCESSING_CONFIG) || !strings.Contains(err.Error(), "AGG_SET_FREQUENCY") {
		t.Fatalf("err = %v, want PROCESSING_CONFIG naming AGG_SET_FREQUENCY", err)
	}
}
