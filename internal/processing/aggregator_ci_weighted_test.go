package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// CI critical value + weighted AGG_CI_LOWER / AGG_CI_UPPER
// (weighting-inferential E5-S1).

// TestNormalCriticalTwoSided_MatchesR pins the two-sided normal
// critical value to R 4.6.1 qnorm(1 − α/2) at a few ulp. It replaced a
// Winitzki inverse-erf approximation (U36 #205) that was ~4.7e-4
// relative too small at α = 0.05 — far outside this tolerance.
func TestNormalCriticalTwoSided_MatchesR(t *testing.T) {
	for _, c := range []struct{ alpha, want float64 }{
		// R 4.6.1: sprintf("%.17g", qnorm(1 - a/2))
		{0.2, 1.2815515655446006},
		{0.1, 1.6448536269514715},
		{0.05, 1.9599639845400534},
		{0.01, 2.5758293035488999},
		{0.001, 3.2905267314919247},
	} {
		got := normalCriticalTwoSided(c.alpha)
		if rel := math.Abs(got-c.want) / c.want; rel > 1e-14 {
			t.Errorf("normalCriticalTwoSided(%v) = %.17g, R qnorm %.17g (rel %.3g)", c.alpha, got, c.want, rel)
		}
	}
}

func ciSpec(op types.AggregationType, conf string, w types.SlotWeight) *types.Aggregation {
	return &types.Aggregation{Type: op, Field: "value", Weight: w,
		Params: json.RawMessage(`{"confidence":` + conf + `}`)}
}

func newCI(t *testing.T, spec *types.Aggregation) *ciAggregator {
	t.Helper()
	bound := ciLower
	if spec.Type == types.AGG_CI_UPPER {
		bound = ciUpper
	}
	a, err := newCIAggregator(bound)(spec, cohortSchema())
	if err != nil {
		t.Fatal(err)
	}
	return a.(*ciAggregator)
}

type ciRow struct{ x, w float64 }

// ciRun folds rows through UpdateRow (streaming) and returns the bound
// and the components.
func ciRun(t *testing.T, spec *types.Aggregation, rows []ciRow) (float64, map[string]any) {
	t.Helper()
	a := newCI(t, spec)
	for _, r := range rows {
		if err := a.UpdateRow(cohortRec(r.x, r.w, 0, 0, nil), "value"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	comp, _ := a.Components()
	return got, comp
}

// ciBuffered runs the buffered Aggregate path.
func ciBuffered(t *testing.T, spec *types.Aggregation, rows []ciRow) float64 {
	t.Helper()
	recs := make([]*Record, len(rows))
	for i, r := range rows {
		recs[i] = cohortRec(r.x, r.w, 0, 0, nil)
	}
	got, err := newCI(t, spec).Aggregate(recs, "value")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// ciMerged folds rows into k partials and merges them.
func ciMerged(t *testing.T, spec *types.Aggregation, rows []ciRow, k int) float64 {
	t.Helper()
	parts := make([]*ciAggregator, k)
	for i := range parts {
		parts[i] = newCI(t, spec)
	}
	for i, r := range rows {
		_ = parts[i%k].UpdateRow(cohortRec(r.x, r.w, 0, 0, nil), "value")
	}
	for _, p := range parts[1:] {
		if err := parts[0].MergeOnline(p); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := parts[0].Finalize()
	return got
}

var ciRows = []ciRow{
	{3.5, 2}, {1.25, 1}, {7, 3}, {2, 1}, {9.75, 2}, {4.5, 4}, {6.25, 1}, {5, 3}, {8.5, 2}, {0.75, 1},
}

func ciRel(a, b float64) float64 { return math.Abs(a-b) / math.Abs(b) }

// TestAggregator_CI_CriticalValueBehaviourChange: the bound is
// mean ∓ qnorm(1 − α/2)·s/√n with R's qnorm, and t_critical reports it.
func TestAggregator_CI_CriticalValueBehaviourChange(t *testing.T) {
	rows := []ciRow{{8, 1}, {9, 1}, {10, 1}, {11, 1}, {12, 1}}
	const z = 1.9599639845400534 // R qnorm(0.975)
	se := math.Sqrt(2.5 / 5)
	lo, comp := ciRun(t, ciSpec(types.AGG_CI_LOWER, "0.95", types.SlotWeight{}), rows)
	if ciRel(lo, 10-z*se) > 1e-15 {
		t.Errorf("lower = %.17g, want %.17g", lo, 10-z*se)
	}
	if tc := comp["t_critical"].(float64); ciRel(tc, z) > 1e-15 {
		t.Errorf("t_critical = %.17g, R qnorm(0.975) = %.17g", tc, z)
	}
}

// TestAggregator_CI_WeightedUnityIsBitIdentical: an all-ones weight
// answers the unweighted bound bit for bit under both kinds, on the
// streaming, buffered and merged paths.
func TestAggregator_CI_WeightedUnityIsBitIdentical(t *testing.T) {
	ones := make([]ciRow, len(ciRows))
	for i, r := range ciRows {
		ones[i] = ciRow{r.x, 1}
	}
	for _, op := range []types.AggregationType{types.AGG_CI_LOWER, types.AGG_CI_UPPER} {
		base, baseComp := ciRun(t, ciSpec(op, "0.9", types.SlotWeight{}), ones)
		baseBuf := ciBuffered(t, ciSpec(op, "0.9", types.SlotWeight{}), ones)
		baseMerged := ciMerged(t, ciSpec(op, "0.9", types.SlotWeight{}), ones, 3)
		for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
			w := types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: kind})
			got, comp := ciRun(t, ciSpec(op, "0.9", w), ones)
			if got != base {
				t.Errorf("%s %s streaming: %.17g, unweighted %.17g", op, kind, got, base)
			}
			for k, v := range baseComp {
				if comp[k] != v {
					t.Errorf("%s %s components.%s: %v, unweighted %v", op, kind, k, comp[k], v)
				}
			}
			if got := ciBuffered(t, ciSpec(op, "0.9", w), ones); got != baseBuf {
				t.Errorf("%s %s buffered: %.17g, unweighted %.17g", op, kind, got, baseBuf)
			}
			if got := ciMerged(t, ciSpec(op, "0.9", w), ones, 3); got != baseMerged {
				t.Errorf("%s %s merged: %.17g, unweighted %.17g", op, kind, got, baseMerged)
			}
		}
	}
}

// TestAggregator_CI_FrequencyIsExpansion: integer frequency weights
// answer the unweighted bound on the physically repeated rows.
func TestAggregator_CI_FrequencyIsExpansion(t *testing.T) {
	var expanded []ciRow
	for _, r := range ciRows {
		for range int(r.w) {
			expanded = append(expanded, ciRow{r.x, 1})
		}
	}
	w := types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindFrequency})
	for _, op := range []types.AggregationType{types.AGG_CI_LOWER, types.AGG_CI_UPPER} {
		want, wantComp := ciRun(t, ciSpec(op, "0.95", types.SlotWeight{}), expanded)
		got, comp := ciRun(t, ciSpec(op, "0.95", w), ciRows)
		if ciRel(got, want) > 1e-13 {
			t.Errorf("%s: %.17g, expanded %.17g", op, got, want)
		}
		for _, k := range []string{"mean", "stderr", "t_critical"} {
			if ciRel(comp[k].(float64), wantComp[k].(float64)) > 1e-13 {
				t.Errorf("%s components.%s: %v, expanded %v", op, k, comp[k], wantComp[k])
			}
		}
		if got := ciBuffered(t, ciSpec(op, "0.95", w), ciRows); ciRel(got, want) > 1e-13 {
			t.Errorf("%s buffered: %.17g, expanded %.17g", op, got, want)
		}
		if got := ciMerged(t, ciSpec(op, "0.95", w), ciRows, 4); ciRel(got, want) > 1e-13 {
			t.Errorf("%s merged: %.17g, expanded %.17g", op, got, want)
		}
	}
}

// TestAggregator_CI_ProbabilityKish: under kind probability the bound
// is the weighted mean ∓ z·√(s²/n_eff), s² = c·M2/(n_eff − 1) on
// w* = w·n_eff/Σw (closed form), invariant under a common weight scale
// and different from the frequency bound on the same weights.
func TestAggregator_CI_ProbabilityKish(t *testing.T) {
	var sw, sww, swx float64
	for _, r := range ciRows {
		sw += r.w
		sww += r.w * r.w
		swx += r.w * r.x
	}
	mean := swx / sw
	var m2 float64
	for _, r := range ciRows {
		m2 += r.w * (r.x - mean) * (r.x - mean)
	}
	neff := sw * sw / sww
	s2 := (neff / sw) * m2 / (neff - 1)
	z := normalCriticalTwoSided(0.05)
	want := mean - z*math.Sqrt(s2/neff)

	prob := types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindProbability})
	got, comp := ciRun(t, ciSpec(types.AGG_CI_LOWER, "0.95", prob), ciRows)
	if ciRel(got, want) > 1e-13 {
		t.Errorf("probability lower = %.17g, closed form %.17g", got, want)
	}
	if ciRel(comp["stderr"].(float64), math.Sqrt(s2/neff)) > 1e-13 {
		t.Errorf("stderr = %v, want %v", comp["stderr"], math.Sqrt(s2/neff))
	}
	if got := ciMerged(t, ciSpec(types.AGG_CI_LOWER, "0.95", prob), ciRows, 3); ciRel(got, want) > 1e-13 {
		t.Errorf("merged = %.17g, closed form %.17g", got, want)
	}
	scaled := make([]ciRow, len(ciRows))
	for i, r := range ciRows {
		scaled[i] = ciRow{r.x, r.w * 0.37}
	}
	if got2, _ := ciRun(t, ciSpec(types.AGG_CI_LOWER, "0.95", prob), scaled); ciRel(got2, got) > 1e-13 {
		t.Errorf("weights × 0.37: %.17g, unscaled %.17g", got2, got)
	}
	freq, _ := ciRun(t, ciSpec(types.AGG_CI_LOWER, "0.95", types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindFrequency})), ciRows)
	if ciRel(freq, got) < 1e-6 {
		t.Errorf("frequency %.17g equals probability %.17g: the Kish N* did not apply", freq, got)
	}
}

// TestAggregator_CI_WeightedRowHandling: an invalid or zero weight
// contributes nothing, and N* ≤ 1 is the undefined (NaN) bound.
func TestAggregator_CI_WeightedRowHandling(t *testing.T) {
	freq := types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindFrequency})
	clean, _ := ciRun(t, ciSpec(types.AGG_CI_UPPER, "0.95", freq), ciRows)
	dirty := append(append([]ciRow(nil), ciRows...), ciRow{1000, 0}, ciRow{-1000, -2}, ciRow{500, math.NaN()}, ciRow{700, 1.5})
	if got, _ := ciRun(t, ciSpec(types.AGG_CI_UPPER, "0.95", freq), dirty); got != clean {
		t.Errorf("invalid / zero weights changed the bound: %.17g, clean %.17g", got, clean)
	}
	if got, _ := ciRun(t, ciSpec(types.AGG_CI_UPPER, "0.95", freq), []ciRow{{4, 1}}); !math.IsNaN(got) {
		t.Errorf("Σw = 1: %v, want NaN", got)
	}
	// One row of weight 3 is three identical rows: zero spread, bound = mean.
	if got, _ := ciRun(t, ciSpec(types.AGG_CI_UPPER, "0.95", freq), []ciRow{{4, 3}}); got != 4 {
		t.Errorf("one row × 3: %v, want 4", got)
	}
	prob := types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindProbability})
	if got, _ := ciRun(t, ciSpec(types.AGG_CI_UPPER, "0.95", prob), []ciRow{{4, 7.5}}); !math.IsNaN(got) {
		t.Errorf("n_eff = 1: %v, want NaN", got)
	}
}
