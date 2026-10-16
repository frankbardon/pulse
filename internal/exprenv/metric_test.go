package exprenv

import (
	stderrors "errors"
	"math"
	"strconv"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/metric"
)

// TestExprMetrics_ResolveThroughRegistry: every expression metric
// function resolves its metric through linalg/metric (consumer expr) and
// returns exactly the registry's value — dist is euclidean (the element
// sum rescaled by p/p_observed when a pair is missing, R stats::dist()),
// cosine the cosine SIMILARITY — bit for bit.
func TestExprMetrics_ResolveThroughRegistry(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		name         string
		a, b         []float64
		dist, cosine float64
	}{
		{"empty", []float64{}, []float64{}, nan, nan},
		{"no-complete-pair", []float64{nan, 1}, []float64{2, nan}, nan, nan},
		{"plain", []float64{1, 2, 3}, []float64{4, 5, 6}, math.Sqrt(27), 32 / (math.Sqrt(14) * math.Sqrt(77))},
		// p = 4, p_observed = 2: (0² + 9²) · 4/2 = 162, R dist() convention.
		{"pair-dropped-either-side", []float64{1, nan, 3, 10}, []float64{1, 7, nan, 1}, math.Sqrt(162), 11 / (math.Sqrt(101) * math.Sqrt(2))},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}, math.Sqrt2, 0},
		{"parallel", []float64{2, 4}, []float64{1, 2}, math.Sqrt(5), 1},
		{"zero-norm-left", []float64{0, 0}, []float64{1, 2}, math.Sqrt(5), nan},
		{"zero-norm-right", []float64{3, 4}, []float64{0, 0}, 5, nan},
		{"zero-norm-after-drop", []float64{0, 5}, []float64{1, nan}, math.Sqrt(2), nan},
		{"underflowed-norm-left", []float64{1e-200}, []float64{1}, 1, nan},
		{"infinity is ordinary", []float64{math.Inf(1), 0}, []float64{0, 0}, math.Inf(1), nan},
	}
	same := func(got any, want float64) bool {
		g, ok := got.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(want) {
			return math.IsNaN(g)
		}
		return closeTo(g, want) || g == want
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := builtinImpls["dist"](c.a, c.b)
			if err != nil || !same(got, c.dist) {
				t.Errorf("dist = %v, %v; want %v", got, err, c.dist)
			}
			euclid, _ := metric.Lookup("euclidean")
			if want, _ := euclid.Eval(metric.Distance, c.a, c.b, nil); !math.IsNaN(want) && got != want {
				t.Errorf("dist %v is not the registry's euclidean %v bit for bit", got, want)
			}
			got, err = builtinImpls["cosine"](c.a, c.b)
			if err != nil || !same(got, c.cosine) {
				t.Errorf("cosine = %v, %v; want %v", got, err, c.cosine)
			}
			cos, _ := metric.Lookup("cosine")
			if want, _ := cos.Eval(metric.Similarity, c.a, c.b, nil); !math.IsNaN(want) && got != want {
				t.Errorf("cosine %v is not the registry's cosine similarity %v bit for bit", got, want)
			}
		})
	}
	for _, fn := range []string{"dist", "cosine", "dot"} {
		_, err := builtinImpls[fn]([]float64{1, 2}, []float64{1})
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_VECTOR_LENGTH_MISMATCH || ce.Details["left"] != 2 || ce.Details["right"] != 1 {
			t.Errorf("%s length mismatch = %v, want PROCESSING_VECTOR_LENGTH_MISMATCH 2/1", fn, err)
		}
	}
}

// TestExprMetrics_SetKernels pins the set similarities, evaluated by
// the registry over label counts: NaN denominators stay NaN, hamming is
// an int count.
func TestExprMetrics_SetKernels(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
		o                      metric.SetCounts
		jaccard, dice, overlap float64
		hamming                int
	}{
		{metric.SetCounts{A: 3, B: 3, Inter: 2}, 2.0 / 4, 4.0 / 6, 2.0 / 3, 2},
		{metric.SetCounts{A: 2, B: 1, Inter: 0}, 0, 0, 0, 3},
		{metric.SetCounts{A: 2, B: 2, Inter: 2}, 1, 1, 1, 0},
		{metric.SetCounts{A: 4, B: 1, Inter: 1}, 1.0 / 4, 2.0 / 5, 1, 3},
		{metric.SetCounts{}, nan, nan, nan, 0},
		{metric.SetCounts{A: 0, B: 3, Inter: 0}, 0, 0, nan, 3},
		{metric.SetCounts{A: 256, B: 256, Inter: 256}, 1, 1, 1, 0},
		{metric.SetCounts{A: 256, B: 1, Inter: 1}, 1.0 / 256, 2.0 / 257, 1, 255},
	}
	near := func(got, want float64) bool {
		if math.IsNaN(want) {
			return math.IsNaN(got)
		}
		return closeTo(got, want)
	}
	for i, tc := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			for _, k := range []struct {
				fn   string
				r    exprMetricRef
				want float64
			}{
				{"jaccard", exprMetric("jaccard", metric.Similarity), tc.jaccard},
				{"dice", exprMetric("dice", metric.Similarity), tc.dice},
				{"overlap", exprMetric("overlap", metric.Similarity), tc.overlap},
				{"hamming", exprHamming, float64(tc.hamming)},
			} {
				if got, err := setMetric(k.fn, k.r, tc.o); err != nil || !near(got, k.want) {
					t.Errorf("%s(%+v) = %v, %v; want %v", k.fn, tc.o, got, err, k.want)
				}
			}
		})
	}
	if _, err := setMetric("jaccard", exprMetric("jaccard", metric.Similarity), metric.SetCounts{A: 1, B: 1, Inter: 2}); err == nil {
		t.Error("impossible counts accepted")
	}
	got, err := setHammingBuiltin([]any{"a", "b"}, []any{"b", "c", "d"})
	if v, ok := got.(int); err != nil || !ok || v != 3 {
		t.Errorf("hamming = %v (%T), %v; want int 3", got, got, err)
	}
}

// TestLabelSetCounts: distinct members of each list and their
// intersection; duplicates count once, order is irrelevant, inputs are
// never mutated.
func TestLabelSetCounts(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want metric.SetCounts
	}{
		{"disjoint", []string{"a", "b"}, []string{"c"}, metric.SetCounts{A: 2, B: 1}},
		{"partial", []string{"a", "b", "c"}, []string{"b", "c", "d"}, metric.SetCounts{A: 3, B: 3, Inter: 2}},
		{"duplicates count once", []string{"a", "a", "b"}, []string{"b", "b"}, metric.SetCounts{A: 2, B: 1, Inter: 1}},
		{"order irrelevant", []string{"c", "a"}, []string{"a", "c"}, metric.SetCounts{A: 2, B: 2, Inter: 2}},
		{"empty vs empty", nil, []string{}, metric.SetCounts{}},
		{"empty vs some", nil, []string{"a"}, metric.SetCounts{B: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]string(nil), tc.a...)
			if got := labelSetCounts(tc.a, tc.b); got != tc.want {
				t.Errorf("labelSetCounts(%v, %v) = %+v, want %+v", tc.a, tc.b, got, tc.want)
			}
			for i := range in {
				if in[i] != tc.a[i] {
					t.Error("labelSetCounts mutated its input")
				}
			}
		})
	}
}

// TestExprMetrics_ManifestTruthful: the registry's expr consumer lists
// exactly the metrics an expression function exposes, in the forms it
// exposes them — cosine as a similarity only.
func TestExprMetrics_ManifestTruthful(t *testing.T) {
	exposed := map[string]metric.Form{
		"euclidean": metric.Distance, "cosine": metric.Similarity,
		"jaccard": metric.Similarity, "dice": metric.Similarity, "hamming": metric.Distance, "overlap": metric.Similarity,
	}
	accepted := metric.Accepted(metric.ConsumerExpr)
	if len(accepted) != len(exposed) {
		t.Fatalf("expr accepts %v, want exactly %d metrics", accepted, len(exposed))
	}
	for _, name := range accepted {
		f, ok := exposed[name]
		if !ok {
			t.Errorf("expr accepts %s, which no expression function exposes", name)
			continue
		}
		if _, err := metric.ResolveFor(metric.ConsumerExpr, name, f); err != nil {
			t.Errorf("ResolveFor(expr, %s, %s): %v", name, f, err)
		}
	}
	if _, err := metric.ResolveFor(metric.ConsumerExpr, "cosine", metric.Distance); err == nil {
		t.Error("expr resolves cosine as a distance; no expression function exposes it")
	}
}
