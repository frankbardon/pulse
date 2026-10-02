package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Oracle fixture for the AGG_WEIGHTED_MEAN variance components.
//
//	x = [2, 4, 4, 5, 7, 9]
//	w = [1/2, 3/2, 1, 2, 4/5, 6/5]
//
// Constants derived independently in exact rational arithmetic
// (Python fractions.Fraction), not from the engine's recurrence:
//
//	Σw                 = 7
//	mean   = Σw·x / Σw = 187/35
//	m2     = Σw(x−mean)² = 5001/175
//	Σw²                = 479/50
//	weighted_variance  = m2 / (Σw − 1) = 1667/350
//	                     (statsmodels DescrStatsW(x, weights=w, ddof=1).var
//	                     — the frequency-weights convention)
//	n_eff              = (Σw)² / Σw² = 2450/479   (closed-form Kish)
var (
	wvOracleX = []float64{2, 4, 4, 5, 7, 9}
	wvOracleW = []float64{0.5, 1.5, 1.0, 2.0, 0.8, 1.2}
)

const (
	wvOracleSumWeights   = 7.0
	wvOracleMean         = 187.0 / 35.0
	wvOracleM2           = 5001.0 / 175.0
	wvOracleSumWeightsSq = 479.0 / 50.0
	wvOracleVariance     = 1667.0 / 350.0
	wvOracleNEff         = 2450.0 / 479.0
	wvOracleSumWeighted  = 187.0 / 5.0 // mean * Σw
)

// Relative tolerance, not exact equality: the recurrence vs the closed
// form (and arm64 FMA fusion vs amd64) diverge in the last few ULPs.
const wvTol = 1e-12

func wvSpec() *types.Aggregation {
	return &types.Aggregation{
		Type:   types.AGG_WEIGHTED_MEAN,
		Field:  "value",
		Params: json.RawMessage(`{"weight_field":"weight"}`),
	}
}

func wvRecords(xs, ws []float64) []*Record {
	out := make([]*Record, len(xs))
	for i := range xs {
		out[i] = cohortRec(xs[i], ws[i], 0, 0, nil)
	}
	return out
}

func wvNew(t *testing.T) *weightedMeanAggregator {
	t.Helper()
	agg, err := newWeightedMeanAggregator(wvSpec(), cohortSchema())
	if err != nil {
		t.Fatal(err)
	}
	return agg.(*weightedMeanAggregator)
}

func wvComponents(t *testing.T, a *weightedMeanAggregator) map[string]any {
	t.Helper()
	c, err := a.Components()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func wvStream(t *testing.T, records []*Record) map[string]any {
	t.Helper()
	a := wvNew(t)
	for _, r := range records {
		if err := a.UpdateRow(r, "value"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Finalize(); err != nil {
		t.Fatal(err)
	}
	return wvComponents(t, a)
}

func wvBuffered(t *testing.T, records []*Record) map[string]any {
	t.Helper()
	a := wvNew(t)
	if _, err := a.Aggregate(records, "value"); err != nil {
		t.Fatal(err)
	}
	return wvComponents(t, a)
}

func wvClose(got, want float64) bool {
	if want == 0 {
		return math.Abs(got) <= wvTol
	}
	return math.Abs(got-want) <= wvTol*math.Abs(want)
}

func wvAssert(t *testing.T, label string, got map[string]any, want map[string]float64) {
	t.Helper()
	for k, w := range want {
		raw, ok := got[k]
		if !ok {
			t.Errorf("%s: missing component %q", label, k)
			continue
		}
		g, ok := raw.(float64)
		if !ok {
			t.Errorf("%s: %q = %T, want float64", label, k, raw)
			continue
		}
		if !wvClose(g, w) {
			t.Errorf("%s: %q = %.17g, want %.17g", label, k, g, w)
		}
	}
}

func wvOracleWant() map[string]float64 {
	return map[string]float64{
		"sum_weighted":      wvOracleSumWeighted,
		"sum_weights":       wvOracleSumWeights,
		"weighted_mean":     wvOracleMean,
		"m2_weighted":       wvOracleM2,
		"sum_weights_sq":    wvOracleSumWeightsSq,
		"weighted_variance": wvOracleVariance,
		"n_eff":             wvOracleNEff,
	}
}

func TestWeightedMean_VarianceComponents_Oracle(t *testing.T) {
	records := wvRecords(wvOracleX, wvOracleW)
	wvAssert(t, "buffered", wvBuffered(t, records), wvOracleWant())
	wvAssert(t, "streaming", wvStream(t, records), wvOracleWant())
}

// Buffered (Aggregate) and streaming (UpdateRow/Finalize) both run
// foldOne in the same order, so their component maps agree exactly.
func TestWeightedMean_VarianceComponents_BufferedMatchesStreaming(t *testing.T) {
	records := wvRecords(wvOracleX, wvOracleW)
	b := wvBuffered(t, records)
	s := wvStream(t, records)
	if len(b) != len(s) {
		t.Fatalf("buffered %d keys, streaming %d keys", len(b), len(s))
	}
	for k, bv := range b {
		if s[k] != bv {
			t.Errorf("%q: buffered=%v streaming=%v", k, bv, s[k])
		}
	}
}

// Every split point of the fixture — including 0 (merge a full partial
// INTO an empty one: the empty-copy branch) and len (merge an empty
// partial in) — must reproduce the single-pass oracle.
func TestWeightedMean_VarianceComponents_MergeMatchesSinglePass(t *testing.T) {
	records := wvRecords(wvOracleX, wvOracleW)
	for split := 0; split <= len(records); split++ {
		left, right := wvNew(t), wvNew(t)
		for _, r := range records[:split] {
			_ = left.UpdateRow(r, "value")
		}
		for _, r := range records[split:] {
			_ = right.UpdateRow(r, "value")
		}
		if err := left.MergeOnline(right); err != nil {
			t.Fatal(err)
		}
		if _, err := left.Finalize(); err != nil {
			t.Fatal(err)
		}
		wvAssert(t, "split="+itoa(split), wvComponents(t, left), wvOracleWant())
	}
}

// Three-way merge in shard order, head empty — mirrors a shard reduce
// whose first shard contributed no weighted rows.
func TestWeightedMean_VarianceComponents_MergeIntoEmptyHead(t *testing.T) {
	records := wvRecords(wvOracleX, wvOracleW)
	head, mid, tail := wvNew(t), wvNew(t), wvNew(t)
	for _, r := range records[:2] {
		_ = mid.UpdateRow(r, "value")
	}
	for _, r := range records[2:] {
		_ = tail.UpdateRow(r, "value")
	}
	for _, p := range []*weightedMeanAggregator{mid, tail} {
		if err := head.MergeOnline(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := head.Finalize(); err != nil {
		t.Fatal(err)
	}
	wvAssert(t, "empty-head", wvComponents(t, head), wvOracleWant())
}

func TestWeightedMean_VarianceComponents_EdgeCells(t *testing.T) {
	cases := []struct {
		name string
		xs   []float64
		ws   []float64
		want map[string]float64
	}{
		{
			name: "empty",
			want: map[string]float64{
				"m2_weighted": 0, "sum_weights_sq": 0, "weighted_variance": 0, "n_eff": 0,
			},
		},
		{
			// Single row, weight 3: m2 = 0, Σw² = 9, variance 0/(3−1) = 0,
			// n_eff = 3²/9 = 1.
			name: "singleRow",
			xs:   []float64{42}, ws: []float64{3},
			want: map[string]float64{
				"sum_weights": 3, "m2_weighted": 0, "sum_weights_sq": 9, "weighted_variance": 0, "n_eff": 1,
			},
		},
		{
			// Σw = 0.9 ≤ 1: no positive (Σw − 1) denominator, so
			// weighted_variance reports 0 even though m2 > 0.
			// mean = (0.4·1 + 0.5·3)/0.9 = 19/9; m2 = 0.4·(10/9)² + 0.5·(8/9)²
			// = 0.8/0.9 = 8/9; Σw² = 0.41; n_eff = 0.81/0.41 = 81/41.
			name: "sumWeightsBelowOne",
			xs:   []float64{1, 3}, ws: []float64{0.4, 0.5},
			want: map[string]float64{
				"sum_weights": 0.9, "m2_weighted": 8.0 / 9.0, "sum_weights_sq": 0.41,
				"weighted_variance": 0, "n_eff": 81.0 / 41.0,
			},
		},
		{
			// Σw exactly 1 is still ≤ 1 → weighted_variance 0.
			// mean = 2; m2 = 0.5·1 + 0.5·1 = 1; Σw² = 0.5; n_eff = 2.
			name: "sumWeightsExactlyOne",
			xs:   []float64{1, 3}, ws: []float64{0.5, 0.5},
			want: map[string]float64{
				"sum_weights": 1, "m2_weighted": 1, "sum_weights_sq": 0.5,
				"weighted_variance": 0, "n_eff": 2,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records := wvRecords(c.xs, c.ws)
			wvAssert(t, "buffered", wvBuffered(t, records), c.want)
			wvAssert(t, "streaming", wvStream(t, records), c.want)
		})
	}

	// A never-finalized aggregator emits the explicit zero map too.
	got := wvComponents(t, wvNew(t))
	wvAssert(t, "unfrozen", got, map[string]float64{
		"m2_weighted": 0, "sum_weights_sq": 0, "weighted_variance": 0, "n_eff": 0,
	})
}

// Reusing one instance must not leak Σw² across runs: Aggregate resets
// the live accumulators, and Finalize wipes them for the next stream.
func TestWeightedMean_VarianceComponents_ResetBetweenRuns(t *testing.T) {
	records := wvRecords(wvOracleX, wvOracleW)

	a := wvNew(t)
	for i := 0; i < 2; i++ {
		if _, err := a.Aggregate(records, "value"); err != nil {
			t.Fatal(err)
		}
	}
	wvAssert(t, "aggregate-twice", wvComponents(t, a), wvOracleWant())

	s := wvNew(t)
	for i := 0; i < 2; i++ {
		for _, r := range records {
			_ = s.UpdateRow(r, "value")
		}
		if _, err := s.Finalize(); err != nil {
			t.Fatal(err)
		}
	}
	wvAssert(t, "stream-twice", wvComponents(t, s), wvOracleWant())
}
