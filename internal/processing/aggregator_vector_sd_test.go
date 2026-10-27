package processing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// vecSDRows: the AGG_VEC_SD fixture — a null member per element, a
// zero and a null weight (raw-counted, never folded when weighted) and
// integer weights so a frequency weight is valid.
func vecSDRows(t *testing.T) []*Record {
	t.Helper()
	s := vecAggSchema()
	return []*Record{
		vecRec(s, 1.5, 10, 7, 1),
		vecRec(s, 3.25, nan, nan, 3),
		vecRec(s, nan, 20, nan, 0),
		vecRec(s, 5, 30, nan, nan),
		vecRec(s, -2, 12.5, nan, 2),
		vecRec(s, 8, nan, nan, 1),
	}
}

// expandedSD is the reference sample sd: each (x, w) pair as w
// physical copies when frequency (or w = 1 unweighted), else the
// closed form M2 / (Σw − Σw²/Σw) on the two-pass weighted mean. NaN
// when the denominator is not positive.
func expandedSD(xs, ws []float64, probability bool) float64 {
	var sw, sw2, swx float64
	for i, x := range xs {
		sw += ws[i]
		sw2 += ws[i] * ws[i]
		swx += ws[i] * x
	}
	if sw == 0 {
		return math.NaN()
	}
	mean := swx / sw
	m2 := 0.0
	for i, x := range xs {
		m2 += ws[i] * (x - mean) * (x - mean)
	}
	den := sw - 1
	if probability {
		den = sw - sw2/sw
	}
	if !(den > 0) {
		return math.NaN()
	}
	return math.Sqrt(m2 / den)
}

func vecSDWeighted(t *testing.T, kind types.WeightKind, params string) *vecAggregator {
	t.Helper()
	agg := &types.Aggregation{Type: types.AGG_VEC_SD, Fields: []string{"a", "b", "c"},
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})}
	if params != "" {
		agg.Params = json.RawMessage(params)
	}
	a, err := newVecAggregator(agg, vecAggSchema())
	if err != nil {
		t.Fatal(err)
	}
	return a.(*vecAggregator)
}

func vecSDClose(got, want, tol float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	return math.Abs(got-want) <= tol*math.Max(1, math.Abs(want))
}

// TestVecAgg_SDPerElement: AGG_VEC_SD is the per-element sample sd —
// unweighted n − 1, frequency Σw − 1 (= the rep() expansion), and
// probability Σw − Σw²/Σw (Kish) — over the rows whose member answered
// (and, weighted, whose weight is valid and > 0); an element whose N*
// ≤ 1 (one row, or none) is NaN, never 0. Buffered and streaming
// agree bit for bit, and the moment components ride beside the sums.
func TestVecAgg_SDPerElement(t *testing.T) {
	t.Parallel()

	rows := vecSDRows(t)
	// Per member, the contributing (x, w) pairs.
	unA, unB := []float64{1.5, 3.25, 5, -2, 8}, []float64{10, 20, 30, 12.5}
	wA, wAw := []float64{1.5, 3.25, -2, 8}, []float64{1, 3, 2, 1}
	wB, wBw := []float64{10, 12.5}, []float64{1, 2}
	ones := func(n int) []float64 {
		o := make([]float64, n)
		for i := range o {
			o[i] = 1
		}
		return o
	}
	cases := []struct {
		name string
		mk   func() *vecAggregator
		want []float64
		sumW []float64
	}{
		{"unweighted", func() *vecAggregator { return newVecAggForTest(t, types.AGG_VEC_SD, false, "") },
			[]float64{expandedSD(unA, ones(5), false), expandedSD(unB, ones(4), false), nan}, nil},
		{"frequency", func() *vecAggregator { return vecSDWeighted(t, types.WeightKindFrequency, "") },
			[]float64{expandedSD(wA, wAw, false), expandedSD(wB, wBw, false), nan}, []float64{7, 3, 1}},
		{"probability", func() *vecAggregator { return vecSDWeighted(t, types.WeightKindProbability, "") },
			[]float64{expandedSD(wA, wAw, true), expandedSD(wB, wBw, true), nan}, []float64{7, 3, 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var bits [2][]uint64
			for k, arm := range []string{"buffered", "streaming"} {
				a := c.mk()
				var v float64
				var err error
				if arm == "buffered" {
					v, err = a.Aggregate(rows, "")
				} else {
					for _, r := range rows {
						_ = a.UpdateRow(r, "")
					}
					v, err = a.Finalize()
				}
				if err != nil || !math.IsNaN(v) {
					t.Fatalf("%s: scalar = %v, %v; want NaN fallback", arm, v, err)
				}
				rich, _ := a.Rich()
				got := rich.([]float64)
				for i := range got {
					if !vecSDClose(got[i], c.want[i], 1e-13) {
						t.Errorf("%s: element %d = %v, want %v", arm, i, got[i], c.want[i])
					}
					bits[k] = append(bits[k], math.Float64bits(got[i]))
				}
				comp, _ := a.Components()
				for _, key := range []string{"labels", "element_n", "element_sum", "element_mean", "element_m2"} {
					if _, ok := comp[key]; !ok {
						t.Errorf("%s: components lack %s: %v", arm, key, comp)
					}
				}
				_, hasW := comp["element_sum_weights"]
				_, hasW2 := comp["element_sum_weights_sq"]
				if (c.sumW != nil) != hasW || hasW != hasW2 {
					t.Errorf("%s: weighted keys present = %v / %v, want %v", arm, hasW, hasW2, c.sumW != nil)
				}
				if c.sumW != nil && !reflect.DeepEqual(comp["element_sum_weights"], c.sumW) {
					t.Errorf("%s: element_sum_weights = %v, want %v", arm, comp["element_sum_weights"], c.sumW)
				}
			}
			if !reflect.DeepEqual(bits[0], bits[1]) {
				t.Errorf("buffered %x != streaming %x", bits[0], bits[1])
			}
		})
	}
}

// TestVecAgg_SDUndefinedIsNaN: N* ≤ 1 has no positive denominator —
// one unweighted row, Σw ≤ 1 under frequency, every weight on one row
// under probability (n_eff = 1) — so the element is NaN (null on the
// wire), never 0.
func TestVecAgg_SDUndefinedIsNaN(t *testing.T) {
	t.Parallel()

	s := vecAggSchema()
	for _, c := range []struct {
		name string
		mk   func() *vecAggregator
		rows []*Record
	}{
		{"one unweighted row", func() *vecAggregator { return newVecAggForTest(t, types.AGG_VEC_SD, false, "") },
			[]*Record{vecRec(s, 1, 2, 3, 1)}},
		{"frequency sum of weights 1", func() *vecAggregator { return vecSDWeighted(t, types.WeightKindFrequency, "") },
			[]*Record{vecRec(s, 1, 2, 3, 1), vecRec(s, 4, 5, 6, 0)}},
		{"probability one weighted row", func() *vecAggregator { return vecSDWeighted(t, types.WeightKindProbability, "") },
			[]*Record{vecRec(s, 1, 2, 3, 0.25), vecRec(s, 4, 5, 6, 0)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := c.mk()
			if _, err := a.Aggregate(c.rows, ""); err != nil {
				t.Fatal(err)
			}
			rich, _ := a.Rich()
			for i, v := range rich.([]float64) {
				if !math.IsNaN(v) {
					t.Errorf("element %d = %v, want NaN", i, v)
				}
			}
		})
	}
	// Two unweighted rows: defined (the boundary the guard must not cross).
	a := newVecAggForTest(t, types.AGG_VEC_SD, false, "")
	if _, err := a.Aggregate([]*Record{vecRec(s, 1, 2, 3, 1), vecRec(s, 3, 2, 3, 1)}, ""); err != nil {
		t.Fatal(err)
	}
	rich, _ := a.Rich()
	if got := rich.([]float64); !vecSDClose(got[0], math.Sqrt2, 1e-15) || got[1] != 0 {
		t.Errorf("two rows: %v, want [√2 0 0]", got)
	}
}

// TestVecAgg_SDMergeMatchesChan: two partials combined by the Chan
// rule equal the one-pass fold of their rows within rounding — the
// combine is the right formula, not merely a deterministic one.
func TestVecAgg_SDMergeMatchesChan(t *testing.T) {
	t.Parallel()

	s := vecAggSchema()
	rows := vecRandomRows(s, 2*linalg.MergeBlockSize+91, 0, 0, 11)
	for _, kind := range []types.WeightKind{"", types.WeightKindProbability} {
		serial := newVecAggForTest(t, types.AGG_VEC_SD, kind != "", "")
		if kind != "" {
			serial = vecSDWeighted(t, kind, "")
		}
		for _, r := range rows {
			_ = serial.UpdateRow(r, "")
		}
		if _, err := serial.Finalize(); err != nil {
			t.Fatal(err)
		}
		got, _ := serial.Rich()
		// Reference: two-pass per element over the same rows.
		for i, m := range []string{"a", "b", "c"} {
			var xs, ws []float64
			for _, r := range rows {
				x, ok := r.NumericValue(m)
				if !ok {
					continue
				}
				w := 1.0
				if kind != "" {
					w, _ = r.NumericValue("w")
				}
				xs, ws = append(xs, x), append(ws, w)
			}
			want := expandedSD(xs, ws, kind != "")
			if g := got.([]float64)[i]; !vecSDClose(g, want, 1e-11) {
				t.Errorf("kind %q element %d = %v, want %v", kind, i, g, want)
			}
		}
	}
}

// TestVecAgg_MeanSumBitsFrozen pins AGG_VEC_MEAN / AGG_VEC_SUM's
// figures and sum components to the words they produced BEFORE
// AGG_VEC_SD shared their fold (captured by running ef294704's
// aggregator, before E5-S1, on these rows): the moment slices ride only
// AGG_VEC_SD's blocks, so the mean and sum paths run their original
// operation sequence. The words are architecture-stable because both
// sides are FMA-free: the fold (float64(w*x), plain adds, one divide)
// and the input generator (vecRandomRows' float64 barrier). The capture
// was identical on arm64 and linux/amd64; an input built with a fusible
// a*b+c would not be, which is how an arm64-only capture once failed
// amd64 CI.
func TestVecAgg_MeanSumBitsFrozen(t *testing.T) {
	t.Parallel()

	s := vecAggSchema()
	n := 2*linalg.MergeBlockSize + 333
	var rows []*Record
	rows = append(rows, vecRandomRows(s, n, 0, 0, 42)...)
	rows = append(rows, vecRandomRows(s, 517, 1, n, 43)...)
	type frozen struct{ value, sum, sumW []uint64 }
	wantN := []int{8149, 8178, 8113}
	sumWWords := []uint64{0x40b326fe3f170b5e, 0x40b3363500ca8681, 0x40b30ca20b986092}
	unSum := []uint64{0x40f27be32f8697a4, 0xc0f6e84d204c57e5, 0x40f6eb5fa7b46567}
	wSum := []uint64{0x40dd62f0c135e7ab, 0xc0f1d74de585a032, 0x40f376cf655ed91c}
	want := map[types.AggregationType]map[bool]frozen{
		types.AGG_VEC_MEAN: {
			false: {[]uint64{0x402294db361746eb, 0xc026f257266d27a6, 0x40272481c81a6694}, unSum, nil},
			true:  {[]uint64{0x40188cb99f9d2000, 0xc02db78507524d28, 0x4030592e434283cf}, wSum, sumWWords},
		},
		types.AGG_VEC_SUM: {
			false: {unSum, unSum, nil},
			true:  {wSum, wSum, sumWWords},
		},
	}
	words := func(v any) []uint64 {
		f, _ := v.([]float64)
		if f == nil {
			return nil
		}
		out := make([]uint64, len(f))
		for i, x := range f {
			out[i] = math.Float64bits(x)
		}
		return out
	}
	for typ, byW := range want {
		for weighted, w := range byW {
			a := newVecAggForTest(t, typ, weighted, "")
			for _, r := range rows {
				_ = a.UpdateRow(r, "")
			}
			got := vecBits(t, a)
			comp, _ := a.Components()
			if !reflect.DeepEqual(got, w.value) {
				t.Errorf("%s weighted=%v: value words %#x, frozen %#x", typ, weighted, got, w.value)
			}
			if g := words(comp["element_sum"]); !reflect.DeepEqual(g, w.sum) {
				t.Errorf("%s weighted=%v: element_sum words %#x, frozen %#x", typ, weighted, g, w.sum)
			}
			if g := words(comp["element_sum_weights"]); !reflect.DeepEqual(g, w.sumW) {
				t.Errorf("%s weighted=%v: element_sum_weights words %#x, frozen %#x", typ, weighted, g, w.sumW)
			}
			if !reflect.DeepEqual(comp["element_n"], wantN) {
				t.Errorf("%s weighted=%v: element_n %v", typ, weighted, comp["element_n"])
			}
			for _, k := range []string{"element_mean", "element_m2", "element_sum_weights_sq"} {
				if _, ok := comp[k]; ok {
					t.Errorf("%s carries AGG_VEC_SD's %s", typ, k)
				}
			}
		}
	}
}
