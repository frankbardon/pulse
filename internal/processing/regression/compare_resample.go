package regression

import (
	"math"
	"slices"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// compare_resample.go: a resampled fit's comparison figures (U69 #642).
// A Wald comparison of a resampled fit reads the covariance its
// coefficient table reads — the jackknife / bootstrap replicate
// covariance, whose √diag is the reported standard error — on χ² (the
// resampled table is Wald z, internal/regcompare). The slot's base fit
// still computes the figures (the deviance, residual df and dispersion
// an F or likelihood ratio reads, unchanged by a resample); a
// ResampledFigures relay holds them until the replicate run has
// summarised, then offers them with the replicate covariance in place
// of the analytical one.

// ResampledFigures relays a resampled slot's comparison figures: the
// base fit offers into the hold HoldFigures returns, and once the
// replicate run is summarised (release) the figures reach the real
// offer with Covariance the replicate covariance over the figures' own
// Terms and WaldDF 0. A slot whose resample fails never releases — the
// fit fails, and the comparison with it (regcompare.Compute reads the
// fit's error first).
type ResampledFigures struct {
	offer func(*model.RegressionFigures)
	held  *model.RegressionFigures
}

// HoldFigures returns the offer a slot's base fit gets and the relay
// that releases it: offer itself and a nil relay when offer is nil or
// spec carries no Resample (the base fit's figures are then the fit's),
// else a hold the relay releases after the replicate run.
func HoldFigures(spec *types.RegressionSpec, offer func(*model.RegressionFigures)) (func(*model.RegressionFigures), *ResampledFigures) {
	if offer == nil || spec.Resample == "" {
		return offer, nil
	}
	r := &ResampledFigures{offer: offer}
	return func(g *model.RegressionFigures) { r.held = g }, r
}

// release offers the held figures with the run's full replicate
// covariance (keys the replicate vector's coefficient keys, in its
// order): each figure term reads its key's row and column; a term the
// replicates do not carry reads NaN (a Wald test over it is NaN, never
// a silent analytical figure). A no-op on a nil relay or when the base
// fit offered nothing.
func (r *ResampledFigures) release(keys []string, run *replicateRun) {
	if r == nil || r.held == nil {
		return
	}
	g := *r.held
	r.held = nil
	cov := run.fullCov()
	idx := make([]int, len(g.Terms))
	for a, t := range g.Terms {
		idx[a] = slices.Index(keys, t)
	}
	out := make([][]float64, len(g.Terms))
	for a := range out {
		out[a] = make([]float64, len(g.Terms))
		for c := range out[a] {
			if idx[a] < 0 || idx[c] < 0 {
				out[a][c] = math.NaN()
				continue
			}
			out[a][c] = cov[idx[a]][idx[c]]
		}
	}
	g.Covariance, g.WaldDF = out, 0
	r.offer(&g)
}
