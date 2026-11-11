package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Ordered-categorical MIN / MAX / MEDIAN / PERCENTILE (U66 E9-S2, #718).

// ordSchema: sat is categorical_u8 with a dictionary deliberately out
// of the scale's order (mid, low, high, na — codes 0..3); g a grouping
// column; w a weight; score an f64.
func ordSchema() *encoding.Schema {
	d := encoding.NewDictionary()
	for _, l := range []string{"mid", "low", "high", "na"} {
		d.Add(l)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "sat", Type: encoding.FieldTypeCategoricalU8, Dictionary: d, Nullable: true},
		{Name: "g", Type: encoding.FieldTypeU8},
		{Name: "w", Type: encoding.FieldTypeF64},
		{Name: "score", Type: encoding.FieldTypeF64},
	}}
}

const (
	cMid, cLow, cHigh, cNA = 0.0, 1.0, 2.0, 3.0
	cNull                  = -1.0 // a null sat
)

func ordRecords(codes, weights []float64) []*Record {
	s := ordSchema()
	out := make([]*Record, len(codes))
	for i, c := range codes {
		w := 1.0
		if weights != nil {
			w = weights[i]
		}
		vals := map[string]float64{"g": float64(i % 2), "w": w, "score": float64(i)}
		var nulls map[string]bool
		if c == cNull {
			nulls = map[string]bool{"sat": true}
		} else {
			vals["sat"] = c
		}
		out[i] = NewRecordWithNulls(s, vals, nulls)
	}
	return out
}

func ordParams(t *testing.T, kv map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(kv)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ordRun runs one aggregation over records (buffered) and returns its
// figure and its operator components.
func ordRun(t *testing.T, agg *types.Aggregation, records []*Record, weight *types.WeightSpec) (float64, map[string]any) {
	t.Helper()
	agg.Label = "x"
	req := &types.Request{Weight: weight, Aggregations: []*types.Aggregation{agg}}
	resp, err := NewProcessor(ordSchema()).processRecords(context.Background(), StampWeights(req, nil), records)
	if err != nil {
		t.Fatalf("%s: %v", agg.Type, err)
	}
	v, _ := resp.Data[0]["x"].(float64)
	if resp.Components == nil || len(resp.Components.Aggregations) != 1 {
		t.Fatalf("components missing: %+v", resp.Components)
	}
	return v, resp.Components.Aggregations[0].Operator
}

// TestOrderedAgg_Table: dictionary order and a levels reorder give
// different medians; an even n picks the lower level (type 1);
// percentile 0 / 100 are the MIN / MAX levels; unlisted rows are
// dropped and counted; an all-null input is null.
func TestOrderedAgg_Table(t *testing.T) {
	t.Parallel()

	ordered := map[string]any{"ordered": true}
	scale := map[string]any{"levels": []string{"low", "mid", "high"}}
	with := func(m map[string]any, k string, v any) map[string]any {
		out := map[string]any{k: v}
		for kk, vv := range m {
			out[kk] = vv
		}
		return out
	}
	// low low mid high high high: dictionary order mid < low < high puts
	// the 3rd of 6 at low; the scale low < mid < high puts it at mid
	// (the 4th, high, is never reached: type 1 takes the lower middle).
	six := []float64{cLow, cLow, cMid, cHigh, cHigh, cHigh}
	withNA := append(append([]float64{}, six...), cNA, cNA, cNull)
	cases := []struct {
		name     string
		op       types.AggregationType
		params   map[string]any
		codes    []float64
		want     float64
		unlisted int
	}{
		{"median dictionary order", types.AGG_MEDIAN, ordered, six, cLow, 0},
		{"median levels order", types.AGG_MEDIAN, scale, six, cMid, 0},
		{"percentile 0 is the min level", types.AGG_PERCENTILE, with(scale, "percentile", 0), six, cLow, 0},
		{"percentile 100 is the max level", types.AGG_PERCENTILE, with(scale, "percentile", 100), six, cHigh, 0},
		{"percentile 50 is the median", types.AGG_PERCENTILE, with(scale, "percentile", 50), six, cMid, 0},
		{"min levels order", types.AGG_MIN, scale, six, cLow, 0},
		{"min dictionary order", types.AGG_MIN, ordered, six, cMid, 0},
		{"max levels order", types.AGG_MAX, scale, six, cHigh, 0},
		{"unlisted dropped and counted", types.AGG_MEDIAN, scale, withNA, cMid, 2},
		{"unlisted dropped from max", types.AGG_MAX, scale, withNA, cHigh, 2},
		{"dictionary order lists every label", types.AGG_MAX, ordered, withNA, cNA, 0},
		{"all null is null", types.AGG_MEDIAN, scale, []float64{cNull, cNull}, math.NaN(), 0},
		{"all null min is null", types.AGG_MIN, scale, []float64{cNull}, math.NaN(), 0},
		{"only unlisted is null", types.AGG_PERCENTILE, with(scale, "percentile", 25), []float64{cNA, cNull}, math.NaN(), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, comp := ordRun(t, &types.Aggregation{Type: c.op, Field: "sat", Params: ordParams(t, c.params)}, ordRecords(c.codes, nil), nil)
			if math.IsNaN(c.want) != math.IsNaN(got) || (!math.IsNaN(c.want) && got != c.want) {
				t.Fatalf("%s = %v, want %v", c.op, got, c.want)
			}
			if n, _ := comp["n_unlisted"].(int); n != c.unlisted {
				t.Fatalf("n_unlisted = %v, want %d (components %v)", comp["n_unlisted"], c.unlisted, comp)
			}
		})
	}
}

// TestOrderedAgg_ROracle checks the ordered percentile against R:
//
//	x <- c("c","a","e","b","b","d","a","c","c","e","b","a","d","d","c","b","e","a","c","b")
//	L <- c("e","c","a","d","b")
//	quantile(as.integer(factor(x, levels = L)), p, type = 1)
//	# 0%..100%: 1 1 1 1 2 2 2 2 3 3 3 4 5 5
//
// over p = 0, .05, .10, .15, .20, .25, .30, .35, .50, .55, .60, .75,
// .95, 1. R's figure is a 1-based rank r; Pulse's the dictionary index
// of L[r].
func TestOrderedAgg_ROracle(t *testing.T) {
	t.Parallel()

	d := encoding.NewDictionary()
	for _, l := range []string{"a", "b", "c", "d", "e"} {
		d.Add(l)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeCategoricalU8, Dictionary: d}}}
	code := map[string]float64{"a": 0, "b": 1, "c": 2, "d": 3, "e": 4}
	x := []string{"c", "a", "e", "b", "b", "d", "a", "c", "c", "e", "b", "a", "d", "d", "c", "b", "e", "a", "c", "b"}
	records := make([]*Record, len(x))
	for i, l := range x {
		records[i] = NewRecord(schema, map[string]float64{"x": code[l]})
	}
	levels := []string{"e", "c", "a", "d", "b"}
	ps := []float64{0, 5, 10, 15, 20, 25, 30, 35, 50, 55, 60, 75, 95, 100}
	rRank := []int{1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 4, 5, 5}
	for i, p := range ps {
		req := &types.Request{Aggregations: []*types.Aggregation{{
			Type: types.AGG_PERCENTILE, Field: "x", Label: "q",
			Params: ordParams(t, map[string]any{"percentile": p, "levels": levels}),
		}}}
		resp, err := NewProcessor(schema).processRecords(context.Background(), req, records)
		if err != nil {
			t.Fatal(err)
		}
		want := code[levels[rRank[i]-1]]
		if got := resp.Data[0]["q"]; got != want {
			t.Errorf("p%v = %v, want %v (R rank %d)", p, got, want, rRank[i])
		}
	}
}

// TestOrderedAgg_Weighted: the lowest level whose cumulative Σw reaches
// p·Σw. Under the scale low < mid < high the weights put 1 on low, 1
// on mid and 3 on high (Σw 5): the median target 2.5 is first reached
// at high, where the unweighted count (1, 1, 1) answers mid. A
// probability and a frequency weight agree (a share is scale-free).
func TestOrderedAgg_Weighted(t *testing.T) {
	t.Parallel()

	codes := []float64{cLow, cMid, cHigh}
	ws := []float64{1, 1, 3}
	params := ordParams(t, map[string]any{"levels": []string{"low", "mid", "high"}})
	if got, _ := ordRun(t, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "sat", Params: params}, ordRecords(codes, ws), nil); got != cMid {
		t.Fatalf("unweighted median = %v, want mid", got)
	}
	for _, kind := range []types.WeightKind{types.WeightKindProbability, types.WeightKindFrequency} {
		got, comp := ordRun(t, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "sat", Params: params}, ordRecords(codes, ws), &types.WeightSpec{Field: "w", Kind: kind})
		if got != cHigh {
			t.Fatalf("%s weighted median = %v, want high (components %v)", kind, got, comp)
		}
	}
	// p40 target 2: low + mid reach it exactly.
	p40 := ordParams(t, map[string]any{"percentile": 40, "levels": []string{"low", "mid", "high"}})
	if got, _ := ordRun(t, &types.Aggregation{Type: types.AGG_PERCENTILE, Field: "sat", Params: p40}, ordRecords(codes, ws), &types.WeightSpec{Field: "w"}); got != cMid {
		t.Fatalf("weighted p40 = %v, want mid", got)
	}
}

// TestOrderedAgg_Components: each operator keeps its keys plus
// n_unlisted.
func TestOrderedAgg_Components(t *testing.T) {
	t.Parallel()

	codes := []float64{cLow, cMid, cHigh, cNA}
	scale := map[string]any{"levels": []string{"low", "mid", "high"}}
	_, comp := ordRun(t, &types.Aggregation{Type: types.AGG_PERCENTILE, Field: "sat", Params: ordParams(t, map[string]any{"percentile": 90, "levels": scale["levels"]})}, ordRecords(codes, nil), nil)
	if comp["method"] != orderedMethod || comp["value"] != cHigh || comp["position"] != 2 || comp["n_unlisted"] != 1 || comp["p"] != 90.0 {
		t.Fatalf("percentile components %v", comp)
	}
	_, comp = ordRun(t, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "sat", Params: ordParams(t, scale)}, ordRecords(codes, nil), nil)
	if comp["median"] != cMid || comp["position_low"] != 1 || comp["position_high"] != 1 || comp["n_unlisted"] != 1 {
		t.Fatalf("median components %v", comp)
	}
	_, comp = ordRun(t, &types.Aggregation{Type: types.AGG_MIN, Field: "sat", Params: ordParams(t, scale)}, ordRecords(codes, nil), nil)
	if comp["min"] != cLow || comp["n_unlisted"] != 1 {
		t.Fatalf("min components %v", comp)
	}
}

// TestOrderedAgg_ExtremeStreamsAndMerges: ordered MIN / MAX keep their
// class — the streaming path runs them and agrees with the buffered
// figure, and two partial states merge to the whole.
func TestOrderedAgg_ExtremeStreamsAndMerges(t *testing.T) {
	t.Parallel()

	// An unlisted na on each side of the split (index 2 left, 4 right).
	codes := []float64{cHigh, cMid, cNA, cHigh, cNA, cLow, cNull, cMid}
	scale := ordParams(t, map[string]any{"levels": []string{"low", "mid", "high"}})
	for _, op := range []types.AggregationType{types.AGG_MIN, types.AGG_MAX} {
		agg := &types.Aggregation{Type: op, Field: "sat", Params: scale, Label: "x"}
		buffered, _ := ordRun(t, &types.Aggregation{Type: op, Field: "sat", Params: scale}, ordRecords(codes, nil), nil)

		proc := NewProcessor(ordSchema())
		resp, err := proc.Process(context.Background(), &types.Request{Aggregations: []*types.Aggregation{agg}}, NewSliceIterator(ordRecords(codes, nil)))
		if err != nil {
			t.Fatal(err)
		}
		if proc.LastPath() == PathBuffered {
			t.Fatalf("%s ordered ran buffered; it must keep streaming", op)
		}
		if got := resp.Data[0]["x"]; got != buffered {
			t.Fatalf("%s streaming %v != buffered %v", op, got, buffered)
		}

		build := func() *orderedExtremeAggregator {
			a, err := aggregatorRegistry[op](agg, ordSchema())
			if err != nil {
				t.Fatal(err)
			}
			return a.(*orderedExtremeAggregator)
		}
		left, right := build(), build()
		recs := ordRecords(codes, nil)
		for i, r := range recs {
			target := left
			if i >= 3 {
				target = right
			}
			if err := target.UpdateRow(r, "sat"); err != nil {
				t.Fatal(err)
			}
		}
		if err := left.MergeOnline(right); err != nil {
			t.Fatal(err)
		}
		got, _ := left.Finalize()
		if got != buffered {
			t.Fatalf("%s merged %v != buffered %v", op, got, buffered)
		}
		comp, _ := left.Components()
		if comp["n_unlisted"] != 2 {
			t.Fatalf("%s merged n_unlisted %v, want 2", op, comp["n_unlisted"])
		}
	}
}

// TestOrderedAgg_Grouped: per-group figures under a grouper.
func TestOrderedAgg_Grouped(t *testing.T) {
	t.Parallel()

	// g alternates 0, 1: g0 = low, mid, high ; g1 = low, high, high.
	codes := []float64{cLow, cLow, cMid, cHigh, cHigh, cHigh}
	req := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "sat", Label: "x",
			Params: ordParams(t, map[string]any{"levels": []string{"low", "mid", "high"}})}},
	}
	resp, err := NewProcessor(ordSchema()).processRecords(context.Background(), req, ordRecords(codes, nil))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, row := range resp.Data {
		got[toKey(row["g"])] = row["x"]
	}
	if got["0"] != cMid || got["1"] != cHigh {
		t.Fatalf("grouped medians %v, want g0 mid, g1 high", got)
	}
}

func toKey(v any) string {
	b, _ := json.Marshal(v)
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	return string(b)
}

// TestOrderedAgg_AbsentIsUnchanged: without ordered / levels a
// categorical stays refused and a numeric field reads its numbers — the
// plain constructors, byte for byte; ordered false is absent.
func TestOrderedAgg_AbsentIsUnchanged(t *testing.T) {
	t.Parallel()

	for _, op := range []types.AggregationType{types.AGG_MIN, types.AGG_MAX, types.AGG_MEDIAN, types.AGG_PERCENTILE} {
		variants := []json.RawMessage{nil, json.RawMessage(`{"ordered":false}`), json.RawMessage(`{"ordered":null}`)}
		if op == types.AGG_PERCENTILE {
			// params.percentile is required (D33).
			variants = []json.RawMessage{json.RawMessage(`{"percentile":50}`), json.RawMessage(`{"ordered":false,"percentile":50}`), json.RawMessage(`{"ordered":null,"percentile":50}`)}
		}
		for _, params := range variants {
			_, err := aggregatorRegistry[op](&types.Aggregation{Type: op, Field: "sat", Params: params}, ordSchema())
			if !errors.HasCode(err, errors.PULSE_FIELD_TYPE_UNSUPPORTED) {
				t.Fatalf("%s %s on categorical: %v, want PULSE_FIELD_TYPE_UNSUPPORTED", op, params, err)
			}
			a, err := aggregatorRegistry[op](&types.Aggregation{Type: op, Field: "score", Params: params}, ordSchema())
			if err != nil {
				t.Fatalf("%s %s on f64: %v", op, params, err)
			}
			switch a.(type) {
			case *orderedAggregator, *orderedExtremeAggregator:
				t.Fatalf("%s %s built the ordered form", op, params)
			}
		}
	}
}

// TestOrderedAgg_Refusals: the constructor's refusals (the shared rule's
// codes; internal/orderedcat holds the messages), on the factory and on
// the decimal arm.
func TestOrderedAgg_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		op     types.AggregationType
		field  string
		params string
		reason string
	}{
		{types.AGG_MEDIAN, "score", `{"ordered":true}`, "ordered_needs_categorical"},
		{types.AGG_MIN, "sat", `{"levels":["low","nope"]}`, "unknown_level"},
		{types.AGG_MAX, "sat", `{"levels":["low","low"]}`, "duplicate_level"},
		{types.AGG_PERCENTILE, "sat", `{"percentile":50,"levels":[]}`, "empty_levels"},
		{types.AGG_MEDIAN, "sat", `{"ordered":false,"levels":["low"]}`, "ordered_false_with_levels"},
		{types.AGG_MEDIAN, "sat", `{"ordered":"yes"}`, "invalid_params"},
	}
	for _, c := range cases {
		_, err := aggregatorRegistry[c.op](&types.Aggregation{Type: c.op, Field: c.field, Params: json.RawMessage(c.params)}, ordSchema())
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG || ce.Details["reason"] != c.reason {
			t.Errorf("%s %s: %v, want PROCESSING_CONFIG reason %s", c.op, c.params, err, c.reason)
		}
	}

	// The decimal arm (AGG_MIN over decimal128 never reaches a factory).
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "amt", Type: encoding.FieldTypeDecimal128, Scale: 2}}}
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_MIN, Field: "amt", Label: "x", Params: json.RawMessage(`{"ordered":true}`)}}}
	_, err := NewProcessor(schema).processRecords(context.Background(), req, nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Details["reason"] != "ordered_needs_categorical" {
		t.Fatalf("decimal AGG_MIN ordered: %v, want ordered_needs_categorical", err)
	}
}
