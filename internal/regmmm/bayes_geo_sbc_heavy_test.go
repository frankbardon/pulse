//go:build heavy

package regmmm

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
)

// bayes_geo_sbc_heavy_test.go is simulation-based calibration (Talts et
// al. 2018) of REG_MMM_BAYES's geo-hierarchical model (params.geo, U70
// E2-S3): bayes_sbc_heavy_test.go's harness on a fixed panel of
// sbcGeoGeos geos × two channels × one control, geometric adstock and the
// Hill curve, in both variants — curves shared across the geos, and
// params.per_geo_curves (the curve's parameters pooled too). Each
// replicate draws every θ coordinate from its prior — the per-geo
// intercepts, the control, each pooled class's population value μ (the
// channel's default prior), its scale τ ~ HalfNormal(geoTauSigma) and
// its non-centred offsets η ~ Normal(0, 1), the shared curve and σ —
// simulates every geo's outcome from the model's own mean at that truth
// plus σ·ε, fits it with the operator's path (the panel's MAP start,
// then NUTS at a geo panel's default target_accept) and ranks the truth
// among the thinned draws. A wrong non-centring (a geo's value off
// T(μ + τ·η)), a missing Jacobian on μ or τ, or a sampler that cannot
// cross the hierarchy's funnel bends a histogram; a sampler that
// diverges past sbcGeoMaxDivergent fails the run.
//
// The outcome scale is fixed at 1 (sbcGeoModel), as in the one-market
// run: the default priors are stated on the max-scaled outcome. The
// design is fixed across replicates; replicate r draws from
// PCG(sbcSeed, sbcGeoStream + r) with uniform and detmath variates and
// samples with seed sbcSeed + r, so the verdict is the same on every run.
//
// The panel is short (sbcGeoWeeks per geo) and the run sbcGeoReplicates
// long — half the one-market run's — to keep the heavy tier's wall
// time sane: the hierarchy's 29 (shared curves) or 45 (per-geo curves)
// coordinates cost several times the one-market model per replicate.
// Ten bins of ten expected ranks keep the χ² test's approximation sound.
const (
	sbcGeoGeos       = 3
	sbcGeoWeeks      = 26
	sbcGeoReplicates = 100
	sbcGeoStream     = 0x67656f00 // replicate streams apart from the one-market run's
	// sbcGeoMaxDivergent caps the post-warmup divergent fraction over
	// every replicate. At the one-market target_accept (0.8) the panel's
	// hierarchy diverges in ~2.5% of transitions, concentrated where the
	// intercepts sit low against a high ROI and its pooling scale — not
	// at small τ — and the per_geo_curves run's tau:s:c1 histogram took
	// a bottom-bin excess beyond its truths' own (25 against the 19
	// truths drawn in the prior's bottom decile). The geo default (0.95)
	// keeps it near 0.5%, and that bin at 21.
	sbcGeoMaxDivergent = 0.01
)

// sbcGeoDesign is the fixed panel design: per geo, two channels' weekly
// spend (bursty, dark weeks; the second channel lighter) and one
// control.
func sbcGeoDesign() (spend, controls [][][]float64) {
	rng := rand.New(rand.NewPCG(sbcSeed, 0x67656f736263))
	for range sbcGeoGeos {
		x := [][]float64{make([]float64, sbcGeoWeeks), make([]float64, sbcGeoWeeks)}
		z := make([]float64, sbcGeoWeeks)
		for w := range sbcGeoWeeks {
			if rng.Float64() < 0.7 {
				x[0][w] = float64(rng.Float64()*0.9) + 0.1
			}
			if rng.Float64() < 0.6 {
				x[1][w] = float64(rng.Float64()*0.5) + 0.05
			}
			z[w] = float64(2*rng.Float64()) - 1
		}
		spend = append(spend, x)
		controls = append(controls, [][]float64{z})
	}
	return spend, controls
}

// sbcGeoModel is newBayesGeoModel over the panel's outcomes y (geometric
// adstock, Hill, default priors) with the outcome scale fixed at 1: each
// geo's scaled outcome is y itself and an ROI channel's β conversion
// uses the geo's raw spend total.
func sbcGeoModel(t *testing.T, y [][]float64, spend, controls [][][]float64, perGeoCurves bool) *bayesGeoModel {
	t.Helper()
	m, err := newBayesGeoModel(geoPanel{y: y, spend: spend, controls: controls, perGeoCurves: perGeoCurves},
		sbcMaxLag, defaultBayesPriors(2))
	if err != nil {
		t.Fatal(err)
	}
	m.shape.yScale = 1
	for g := range m.geos {
		gs := &m.geos[g]
		copy(gs.ys, y[g])
		copy(gs.spendShare, gs.spendTotal)
	}
	return m
}

// sbcGeoNames names every θ coordinate of m in layout order.
func sbcGeoNames(m *bayesGeoModel) []string {
	s := m.shape
	names := make([]string, m.Dim())
	class := func(k int) string {
		switch {
		case k == 0:
			return "roi"
		case k <= s.np:
			return s.kernel.Params()[k-1].key
		}
		return s.sat.Params()[k-1-s.np].key
	}
	for g := range m.ng {
		names[m.iIntercept(g)] = fmt.Sprintf("intercept[%d]", g+1)
	}
	for j := range s.nz {
		names[m.iControl(j)] = fmt.Sprintf("control%d", j+1)
	}
	for c := range s.nc {
		for i, kp := range s.kernel.Params() {
			names[m.iAdstock(i, c)] = fmt.Sprintf("%s:c%d", kp.key, c+1)
		}
		for i, cq := range s.sat.Params() {
			names[m.iCurve(i, c)] = fmt.Sprintf("%s:c%d", cq.key, c+1)
		}
		names[m.iEffect(c)] = fmt.Sprintf("roi:c%d", c+1)
		for k := range m.nk {
			names[m.iTau(k, c)] = fmt.Sprintf("tau:%s:c%d", class(k), c+1)
			for g := range m.ng {
				names[m.iEta(k, c, g)] = fmt.Sprintf("eta:%s:c%d[%d]", class(k), c+1, g+1)
			}
		}
	}
	names[m.iSigma()] = "sigma"
	return names
}

// sbcGeoReplicate draws a truth from ref's priors, simulates the panel,
// fits it and returns each θ coordinate's rank (0..sbcKept) with the
// run's divergence count.
func sbcGeoReplicate(t *testing.T, r int, spend, controls [][][]float64, ref *bayesGeoModel) ([]int, int) {
	rng := rand.New(rand.NewPCG(sbcSeed, uint64(sbcGeoStream+r)))
	dim := ref.Dim()
	theta := make([]float64, dim)
	for i := range theta {
		theta[i] = ref.priors[i].transform().Inverse(ref.priors[i].draw(rng))
	}
	sigma := ref.priors[ref.iSigma()].transform().Forward(theta[ref.iSigma()]).Y
	y := make([][]float64, ref.ng)
	for g := range y {
		y[g] = ref.predictGeo(theta, g, spend[g], controls[g], 0)
		for w := range y[g] {
			y[g][w] += float64(sigma * detmath.NormFloat64(rng))
		}
	}
	m := sbcGeoModel(t, y, spend, controls, ref.perGeoCurves)
	init, _ := m.mapStart(mapMaxIter)
	res, err := mcmc.NUTS{TargetAccept: defaultBayesGeoTargetAccept}.Sample(context.Background(), m, uint64(sbcSeed+r),
		mcmc.Options{Chains: 1, Warmup: sbcWarmup, Draws: sbcKept * sbcThin, Init: init})
	if err != nil {
		t.Errorf("replicate %d: %v", r, err)
		return nil, 0
	}
	ranks := make([]int, dim)
	for i := range dim {
		for d := sbcThin - 1; d < len(res.Draws[0][i]); d += sbcThin {
			if res.Draws[0][i][d] < theta[i] {
				ranks[i]++
			}
		}
	}
	return ranks, res.Diagnostics.Chains[0].Divergences
}

// TestMMMBayes_GeoSimulationBasedCalibration: on the default priors, on
// a three-geo × two-channel panel, with the curves shared and with
// params.per_geo_curves, every θ coordinate's SBC rank histogram — each
// geo's intercept, the control, every population value μ, every pooling
// scale τ and every non-centred offset η, the shared curve and σ — is
// uniform.
func TestMMMBayes_GeoSimulationBasedCalibration(t *testing.T) {
	t.Parallel()

	for _, variant := range []struct {
		name         string
		perGeoCurves bool
	}{{"shared_curves", false}, {"per_geo_curves", true}} {
		t.Run(variant.name, func(t *testing.T) {
			t.Parallel()
			sbcGeo(t, variant.perGeoCurves)
		})
	}
}

// sbcGeo is one variant's calibration run.
func sbcGeo(t *testing.T, perGeoCurves bool) {
	spend, controls := sbcGeoDesign()
	// The reference model reads only the design (its outcome, all ones,
	// is unused): the priors, the control centring and the spend scale.
	ones := make([][]float64, sbcGeoGeos)
	for g := range ones {
		ones[g] = make([]float64, sbcGeoWeeks)
		for w := range ones[g] {
			ones[g][w] = 1
		}
	}
	ref := sbcGeoModel(t, ones, spend, controls, perGeoCurves)
	names := sbcGeoNames(ref)

	ranks := make([][]int, sbcGeoReplicates)
	divs := make([]int, sbcGeoReplicates)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for r := range jobs {
				ranks[r], divs[r] = sbcGeoReplicate(t, r, spend, controls, ref)
			}
		})
	}
	for r := range sbcGeoReplicates {
		jobs <- r
	}
	close(jobs)
	wg.Wait()
	if t.Failed() {
		return
	}
	sbcCheckRanks(t, names, ranks, divs)
	total := 0
	for _, d := range divs {
		total += d
	}
	if frac := float64(total) / float64(sbcGeoReplicates*sbcKept*sbcThin); !(frac <= sbcGeoMaxDivergent) {
		t.Errorf("%d post-warmup divergences (%.2f%% of transitions), above the %.0f%% cap", total, 100*frac, 100*sbcGeoMaxDivergent)
	}
}
