package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_reliability_cov_test.go pins reliabilityCov's per-entry Kish
// factor (TODO #270): under probability weights each covariance entry
// carries n_eff/(n_eff − 1) for the rows THAT entry rests on
// (CoMoment.PairNEff), so a pairwise slot no longer borrows one
// slot-wide n_eff, a listwise slot keeps the slot-wide bits, and an
// entry whose own n_eff ≤ 1 is NaN. The R oracle
// (TestMatrixReliability_MatchesROracle / TestMatrixPCA_MatchesROracle,
// the weighted pairwise nulls cases) pins the values end to end.

var probWeight = &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}

// relCovRows is a 3-member battery with scattered nulls and unequal
// weights, so every pair's Kish n_eff differs from the slot's.
var relCovRows = []struct {
	x []float64
	w float64
}{
	{[]float64{1, 2, 3}, 1.5},
	{[]float64{2, math.NaN(), 4}, 0.4},
	{[]float64{3, 5, math.NaN()}, 2.2},
	{[]float64{math.NaN(), 1, 2}, 0.9},
	{[]float64{5, 4, 6}, 1.1},
	{[]float64{2, 2, math.NaN()}, 3.0},
	{[]float64{4, math.NaN(), 1}, 0.7},
	{[]float64{6, 7, 5}, 1.3},
}

func relCovFold(t *testing.T, mode linalg.CoMomentMode) *linalg.CoMoment {
	t.Helper()
	cm, err := linalg.NewCoMoment(3, mode)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range relCovRows {
		cm.Add(r.x, r.w)
	}
	return cm
}

// TestReliabilityCov_PairwisePerEntryNEff: on a pairwise probability
// slot every entry is Cov(0)·n/(n − 1) with n = PairNEff(i, j), bit for
// bit, and at least one entry's n differs from the slot-wide NEff (so
// the slot-wide factor would move it).
func TestReliabilityCov_PairwisePerEntryNEff(t *testing.T) {
	cm := relCovFold(t, linalg.Pairwise)
	c, raw := reliabilityCov(cm, probWeight), cm.Cov(0)
	differs := false
	for i := 0; i < 3; i++ {
		for j := i; j < 3; j++ {
			n := cm.PairNEff(i, j)
			if n != cm.NEff() {
				differs = true
			}
			want := float64(raw.At(i, j) * (n / (n - 1)))
			if got := c.At(i, j); math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("c[%d][%d] = %v, want per-pair %v (pair n_eff %v, slot n_eff %v)", i, j, got, want, n, cm.NEff())
			}
		}
	}
	if !differs {
		t.Fatal("fixture is vacuous: every pair's n_eff equals the slot's")
	}
}

// TestReliabilityCov_ListwiseKeepsSlotWideBits: listwise, PairNEff is the
// slot's NEff for every entry, so the result is bit-identical to the
// slot-wide factor the operator always applied there.
func TestReliabilityCov_ListwiseKeepsSlotWideBits(t *testing.T) {
	cm := relCovFold(t, linalg.Listwise)
	c, raw := reliabilityCov(cm, probWeight), cm.Cov(0)
	f := cm.NEff() / (cm.NEff() - 1)
	for i := 0; i < 3; i++ {
		for j := i; j < 3; j++ {
			want := float64(raw.At(i, j) * f)
			if got := c.At(i, j); math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("c[%d][%d] = %v, want slot-wide %v", i, j, got, want)
			}
		}
	}
}

// TestReliabilityCov_UnweightedAndFrequencyUntouched: the Cov(1) branch
// (already per pair: PairW − 1) is unchanged.
func TestReliabilityCov_UnweightedAndFrequencyUntouched(t *testing.T) {
	cm := relCovFold(t, linalg.Pairwise)
	freq := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	want := cm.Cov(1)
	for _, w := range []*types.WeightSpec{nil, freq} {
		c := reliabilityCov(cm, w)
		for i := 0; i < 3; i++ {
			for j := i; j < 3; j++ {
				if math.Float64bits(c.At(i, j)) != math.Float64bits(want.At(i, j)) {
					t.Errorf("weight %v: c[%d][%d] = %v, want Cov(1) %v", w, i, j, c.At(i, j), want.At(i, j))
				}
			}
		}
	}
}

// TestReliabilityCov_ThinPairIsNaN: a pair whose own rows give n_eff ≤ 1
// (here one shared row) is NaN even though the slot's n_eff is large;
// alpha and the item-total r it reaches are NaN (JSON null), never ±Inf,
// while an item's own variance over its own rows stays defined.
func TestReliabilityCov_ThinPairIsNaN(t *testing.T) {
	cm, err := linalg.NewCoMoment(3, linalg.Pairwise)
	if err != nil {
		t.Fatal(err)
	}
	nan := math.NaN()
	// x0 and x1 share exactly one row; x2 is observed beside both.
	for _, r := range []struct {
		x []float64
		w float64
	}{
		{[]float64{1, 2, 3}, 1.0},
		{[]float64{2, nan, 4}, 1.2},
		{[]float64{4, nan, 1}, 0.8},
		{[]float64{3, nan, 5}, 1.5},
		{[]float64{nan, 5, 2}, 0.6},
		{[]float64{nan, 1, 6}, 1.1},
		{[]float64{nan, 3, 3}, 2.0},
	} {
		cm.Add(r.x, r.w)
	}
	if cm.NEff() <= 1 {
		t.Fatalf("slot n_eff %v: fixture must be thick slot-wide", cm.NEff())
	}
	c := reliabilityCov(cm, probWeight)
	if !math.IsNaN(c.At(0, 1)) {
		t.Errorf("c[0][1] over one shared row = %v, want NaN", c.At(0, 1))
	}
	for i := 0; i < 3; i++ {
		if v := c.At(i, i); math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("c[%d][%d] = %v, want a defined variance over the item's own rows", i, i, v)
		}
	}
	if a := cronbachAlpha(c, []int{0, 1, 2}); !math.IsNaN(a) {
		t.Errorf("alpha = %v, want NaN", a)
	}
	for i := 0; i < 3; i++ {
		if r := correctedItemTotal(c, i); !math.IsNaN(r) {
			t.Errorf("item_total_r[%d] = %v, want NaN", i, r)
		}
	}
}
