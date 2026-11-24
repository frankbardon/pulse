package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// TestHoldFigures_Release: without a resample (or an offer) the base
// fit offers directly; under one the hold keeps the figures back until
// release, which maps each figure term onto the replicate keys — a fit
// through the origin reads its slopes' block, a term the replicates lack
// reads NaN — and sets a Wald z table, leaving the deviance, residual df
// and dispersion untouched. A nil relay and an empty hold are no-ops.
func TestHoldFigures_Release(t *testing.T) {
	t.Parallel()

	var got *model.RegressionFigures
	offer := func(g *model.RegressionFigures) { got = g }
	if o, r := HoldFigures(&types.RegressionSpec{}, offer); r != nil || o == nil {
		t.Fatal("an unresampled slot is held")
	}
	if o, r := HoldFigures(&types.RegressionSpec{Resample: "jackknife"}, nil); r != nil || o != nil {
		t.Fatal("a slot without an offer is held")
	}
	var nilRelay *ResampledFigures
	nilRelay.release(nil, nil) // no panic

	hold, relay := HoldFigures(&types.RegressionSpec{Resample: "bootstrap"}, offer)
	relay.release([]string{InterceptKey}, nil) // nothing held: no offer
	if got != nil {
		t.Fatal("an empty hold offered")
	}
	base := &model.RegressionFigures{Terms: []string{"x2", "x1", "z"}, Coefficients: []float64{1, 2, 3},
		Covariance: [][]float64{{9, 9, 9}, {9, 9, 9}, {9, 9, 9}}, WaldDF: 40, Deviance: 5, DFResidual: 40, Dispersion: 0.5}
	hold(base)
	if got != nil {
		t.Fatal("the hold offered before release")
	}
	reps := []replicate{{coef: []float64{0, 1, 2}}, {coef: []float64{0, 3, 1}}, {coef: []float64{0, 2, 6}}}
	run := newReplicateRun(reps, 3, false, false, func(s float64) float64 { return s / 2 })
	relay.release([]string{InterceptKey, "x1", "x2"}, run)
	if got == nil || got.WaldDF != 0 || got.Deviance != 5 || got.DFResidual != 40 || got.Dispersion != 0.5 {
		t.Fatalf("released %+v", got)
	}
	full := replicateCov([][]float64{{0, 1, 2}, {0, 3, 1}, {0, 2, 6}}, []float64{0, 2, 3}, true, func(s float64) float64 { return s / 2 })
	want := [][]float64{{full[2][2], full[2][1]}, {full[1][2], full[1][1]}}
	for a := 0; a < 2; a++ {
		for c := 0; c < 2; c++ {
			if got.Covariance[a][c] != want[a][c] {
				t.Fatalf("cov[%d][%d] = %v, want %v", a, c, got.Covariance[a][c], want[a][c])
			}
		}
		if !math.IsNaN(got.Covariance[a][2]) || !math.IsNaN(got.Covariance[2][a]) {
			t.Fatalf("an unreplicated term's row %v", got.Covariance[2])
		}
	}
	if full[1][2] == 0 {
		t.Fatal("degenerate fixture: no off-diagonal")
	}
}
