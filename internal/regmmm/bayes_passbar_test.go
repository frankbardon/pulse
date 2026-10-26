package regmmm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_passbar_test.go is REG_MMM_BAYES's pass bar (bayesian-mmm
// E3-S4): on the internal/mmmfixture joint-truth cohort (one market,
// 156 weeks, four channels with known geometric decays and Hill curves,
// price and promo controls) one default fit — 4 chains × (1000 warmup
// + 1000 draws), every week, a pinned rng_seed — must
//
//   - put every channel's true ROI (mmmfixture.TrueROI) inside its 90%
//     posterior interval (outputs.roi.<channel>.lower / upper at the
//     default credible_level);
//   - mix: every R̂ (rank-normalised split, folded max) ≤ passBayesRHat,
//     every bulk ESS ≥ passBayesESSBulk, zero post-warmup divergences —
//     and so carry no sampler-health warning;
//   - give every posterior-median ROI within ±passROITol (relative) of
//     the true ROI, the REG_MMM_HILL pass bar's tolerance;
//   - reproduce the committed hash of the whole result's JSON
//     (testdata/bayes_passbar.sha256) — on arm64 and on CI's amd64, so a
//     cross-architecture divergence of the sampler FAILS here.
//
// TestMMMBayes_PassBarDeterministic adds the in-process determinism
// checks: a rerun and GOMAXPROCS=1 against GOMAXPROCS=N give the same
// bytes.

// The REG_MMM_BAYES pass bar. Never loosen these: tune priors/defaults
// instead (bayes_model.go defaultChannelPriors, bayes.go defaults). The
// ROI bar reuses passROITol.
const (
	// passBayesLevel is the credible level the coverage bar reads: the
	// default credible_level, so the bar runs the default request.
	passBayesLevel   = 0.9
	passBayesRHat    = 1.01
	passBayesESSBulk = 400
	// passBayesDivergences is the post-warmup divergence count allowed.
	passBayesDivergences = 0
)

// passBayesSeed is the pass bar's rng_seed. It is pinned: with rng_seed
// 0 the seed derives from the slot's wire form, which any params change
// would move.
const passBayesSeed = 1

var updateBayesGolden = flag.Bool("mmm.update-bayes-golden", false,
	"rewrite testdata/bayes_passbar.sha256 from this run (then re-run without it)")

// bayesGoldenFile holds the sha256 of the pass bar fit's result JSON.
var bayesGoldenFile = filepath.Join("testdata", "bayes_passbar.sha256")

// jointBayesSpec is the pass bar's slot: every channel a REG_MMM_BAYES
// channel, price and promo linear controls, week the time field; extra
// is spliced into params (empty: the defaults).
func jointBayesSpec(t testing.TB, extra string) *types.RegressionSpec {
	t.Helper()
	names := make([]string, len(mmmfixture.JointChannels))
	for i, c := range mmmfixture.JointChannels {
		names[i] = c.Name
	}
	params := `{"channels":` + mustJSON(t, names) + `,"time":"week"` + extra + `}`
	return &types.RegressionSpec{Type: regmmm.BayesName, Name: "mmm", Target: "sales",
		Predictors: append(names, "price", "promo"), Params: json.RawMessage(params), RNGSeed: passBayesSeed}
}

func jointBayesRequest(s *types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: mmmfixture.JointCohortFile}, Regressions: []*types.RegressionSpec{s}}
}

// bayesCanonical is the default pass bar fit, run once per test binary.
var bayesCanonical struct {
	once    sync.Once
	resp    *types.Response
	data    mmmfixture.JointData
	elapsed time.Duration
	failed  bool
}

func bayesCanonicalRun(t *testing.T) (*types.Response, mmmfixture.JointData) {
	t.Helper()
	bayesCanonical.once.Do(func() {
		bayesCanonical.failed = true
		p, d := jointInstance(t)
		start := time.Now()
		bayesCanonical.resp = process(t, p, jointBayesRequest(jointBayesSpec(t, "")))
		bayesCanonical.elapsed, bayesCanonical.data, bayesCanonical.failed = time.Since(start), d, false
	})
	if bayesCanonical.failed {
		t.Fatal("the REG_MMM_BAYES pass bar fit failed (see the first test that ran it)")
	}
	return bayesCanonical.resp, bayesCanonical.data
}

// bayesPassBar checks a fit against the bar, logging the achieved
// figures, and returns every violation (none: met).
func bayesPassBar(t testing.TB, d mmmfixture.JointData, resp *types.Response) (violations []string) {
	t.Helper()
	bad := func(format string, a ...any) { violations = append(violations, fmt.Sprintf(format, a...)) }
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		return []string{"the fit failed: " + mustJSON(t, resp.Regressions)}
	}
	r := resp.Regressions[0]
	for _, w := range append(append([]*types.ResponseWarning{}, resp.Warnings...), r.Warnings...) {
		bad("warning %s: %s %v", w.Code, w.Message, w.Details)
	}

	dg, ok := r.Outputs["diagnostics"].(map[string]any)
	if !ok {
		return append(violations, "no outputs.diagnostics")
	}
	// The default sampler settings, so the bar is the default fit's.
	for k, want := range map[string]any{"chains": mcmc.DefaultChains, "warmup": mcmc.DefaultWarmup,
		"draws": mcmc.DefaultDraws, "max_tree_depth": mcmc.DefaultMaxTreeDepth} {
		if dg[k] != want {
			bad("diagnostics.%s = %v, want the default %v", k, dg[k], want)
		}
	}
	rhat, essBulk := dg["max_rhat"].(float64), dg["min_ess_bulk"].(float64)
	div, sat := dg["divergences"].(int), dg["treedepth_saturated"].(int)
	if !(rhat <= passBayesRHat) {
		bad("max R̂ %.4f (bar ≤ %.2f)", rhat, passBayesRHat)
	}
	if !(essBulk >= passBayesESSBulk) {
		bad("min bulk ESS %.0f (bar ≥ %d)", essBulk, passBayesESSBulk)
	}
	if div > passBayesDivergences {
		bad("%d post-warmup divergences (bar %d)", div, passBayesDivergences)
	}
	t.Logf("max R̂ %.4f, min bulk ESS %.0f, min tail ESS %.0f, %d divergences, %d tree-depth saturations, step sizes %v",
		rhat, essBulk, dg["min_ess_tail"], div, sat, dg["step_size"])

	roi, ok := r.Outputs["roi"].(map[string]any)
	if !ok {
		return append(violations, "no outputs.roi")
	}
	for _, c := range mmmfixture.JointChannels {
		s, ok := roi[c.Name].(map[string]float64)
		if !ok {
			bad("no outputs.roi.%s", c.Name)
			continue
		}
		truth := d.TrueROI(c.Name)
		if !(s["lower"] <= truth && truth <= s["upper"]) {
			bad("%s true ROI %.4f outside its %.0f%% interval [%.4f, %.4f]", c.Name, truth, 100*passBayesLevel, s["lower"], s["upper"])
		}
		e := s["median"]/truth - 1
		if !(math.Abs(e) <= passROITol) {
			bad("%s median ROI %.4f, truth %.4f (error %+.1f%%, bar ±%.0f%%)", c.Name, s["median"], truth, 100*e, 100*passROITol)
		}
		t.Logf("%-8s ROI median %.4f [%.4f, %.4f] (truth %.4f, %+.2f%%)", c.Name, s["median"], s["lower"], s["upper"], truth, 100*e)
	}
	return violations
}

// resultHash is the sha256 of a regression result's JSON.
func resultHash(t testing.TB, r *types.RegressionResult) string {
	t.Helper()
	sum := sha256.Sum256([]byte(mustJSON(t, r)))
	return hex.EncodeToString(sum[:])
}

// TestMMMBayes_PassBar: the default REG_MMM_BAYES fit on the joint
// fixture meets the bar, its request validates in predict, and its
// result reproduces the committed hash (regenerate with
// -mmm.update-bayes-golden, then re-run without it).
//
// Measured (arm64, rng_seed 1): max R̂ 1.0037, min bulk ESS 1754, min
// tail ESS 1964, 0 divergences, 0 saturations; median ROI errors tv
// +0.54%, search −0.04%, social −1.38%, display −0.12%, every truth
// inside its 90% interval; about 32 s (about 67 s for linux/amd64 under
// emulation, with the same figures and hash).
func TestMMMBayes_PassBar(t *testing.T) {
	resp, d := bayesCanonicalRun(t)
	t.Logf("default fit (156 weeks, 4 channels, %d chains × (%d + %d)): %v",
		mcmc.DefaultChains, mcmc.DefaultWarmup, mcmc.DefaultDraws, bayesCanonical.elapsed)
	for _, v := range bayesPassBar(t, d, resp) {
		t.Error(v)
	}

	p, _ := jointInstance(t)
	pr, err := p.Predict(context.Background(), jointBayesRequest(jointBayesSpec(t, "")))
	if err != nil || !pr.Valid {
		t.Fatalf("predict: %v %s", err, mustJSON(t, pr))
	}

	got := resultHash(t, resp.Regressions[0])
	if *updateBayesGolden {
		if err := os.MkdirAll(filepath.Dir(bayesGoldenFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bayesGoldenFile, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s: %s", bayesGoldenFile, got)
		return
	}
	raw, err := os.ReadFile(bayesGoldenFile)
	if err != nil {
		t.Fatalf("%v (generate it with -mmm.update-bayes-golden)", err)
	}
	if want := strings.TrimSpace(string(raw)); got != want {
		t.Errorf("result hash %s on %s/%s, golden %s: the sampler's bytes diverged (an FMA contraction or a "+
			"non-detmath call, or an intended change: regenerate with -mmm.update-bayes-golden).\ncoefficients %s\noutputs.diagnostics %s",
			got, runtime.GOOS, runtime.GOARCH, want, mustJSON(t, resp.Regressions[0].Coefficients),
			mustJSON(t, resp.Regressions[0].Outputs["diagnostics"]))
	}
}

// TestMMMBayes_PassBarDeterministic: the same seed gives the same bytes.
// A reduced joint-fixture fit (4 chains × (20 + 10); it warns, and the
// warnings are part of the bytes) gives the whole result's JSON byte
// for byte on a rerun and at GOMAXPROCS=1 against GOMAXPROCS=N (the
// chains run in goroutines and merge in chain order). The full-size fit
// is pinned across processes and architectures by TestMMMBayes_PassBar's
// hash.
func TestMMMBayes_PassBarDeterministic(t *testing.T) {
	p, _ := jointInstance(t)
	const reduced = `,"chains":4,"warmup":20,"draws":10`
	run := func() string {
		r := process(t, p, jointBayesRequest(jointBayesSpec(t, reduced))).Regressions[0]
		if r.Error != nil {
			t.Fatalf("reduced fit failed: %s", mustJSON(t, r.Error))
		}
		return mustJSON(t, r)
	}
	// N is at least the chain count, so the parallel run interleaves
	// every chain even on a small runner.
	n := max(runtime.GOMAXPROCS(0), 4)
	prev := runtime.GOMAXPROCS(n)
	defer runtime.GOMAXPROCS(prev)
	start := time.Now()
	want := run()
	t.Logf("reduced fit at GOMAXPROCS=%d: %v", n, time.Since(start))
	if got := run(); got != want {
		t.Error("rerun: bytes differ from the first run")
	}
	runtime.GOMAXPROCS(1)
	start = time.Now()
	got := run()
	runtime.GOMAXPROCS(n)
	t.Logf("reduced fit at GOMAXPROCS=1: %v", time.Since(start))
	if got != want {
		t.Errorf("GOMAXPROCS=1: bytes differ from GOMAXPROCS=%d", n)
	}
}
