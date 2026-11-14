package regmmm_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_sampler_engine_test.go pins params.sampler "hmc" end to end
// (U70 E4-S2): the fit dispatches through the bayesSampler hook to
// mcmc.HMC with the request's path_length, step_size and target_accept;
// static HMC builds no tree, so the diagnostics carry no tree-depth
// figures and the treedepth_saturated rule never fires, while
// divergences are reported with an HMC remedy.

// hmcFastParams is a small HMC fit.
const hmcFastParams = `{"channels":["tv"],"time":"week","chains":1,"warmup":30,"draws":30,"sampler":"hmc","path_length":1.5`

// cheapHMC is what the fast tests run in place of the sampler a fit
// builds: a two-step HMC at a fixed step, so a fit costs milliseconds
// (an adapted warmup this short explores tiny step sizes, each a long
// path). The real HMC fit is the heavy tier's (bayes_hmc_heavy_test.go).
var cheapHMC = mcmc.HMC{PathLength: 0.5, StepSize: 0.25}

// TestMMMBayes_HMCDispatch: sampler hmc builds mcmc.HMC from the
// request's knobs (step_size absent → 0, adapted; target_accept its
// default or the request's), and the default builds NUTS. The fit runs
// cheapHMC in its place (and NUTS as built).
func TestMMMBayes_HMCDispatch(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	var got []mcmc.Sampler
	var settings []regmmm.SamplerSettings
	regmmm.WrapBayesSampler(t, func(s regmmm.SamplerSettings, built mcmc.Sampler) mcmc.Sampler {
		settings = append(settings, s)
		got = append(got, built)
		if s.HMC {
			return cheapHMC
		}
		return built
	})
	p := bayesInstance(t, nil)
	for _, tc := range []struct {
		params string
		want   mcmc.Sampler
	}{
		{hmcFastParams + `}`, mcmc.HMC{PathLength: 1.5, TargetAccept: regmmm.BayesDefaultTargetAccept}},
		{hmcFastParams + `,"target_accept":0.9}`, mcmc.HMC{PathLength: 1.5, TargetAccept: 0.9}},
		{hmcFastParams + `,"step_size":0.25}`, mcmc.HMC{PathLength: 1.5, StepSize: 0.25, TargetAccept: regmmm.BayesDefaultTargetAccept}},
		{`{"channels":["tv"],"time":"week","chains":1,"warmup":20,"draws":20}`, mcmc.NUTS{TargetAccept: regmmm.BayesDefaultTargetAccept}},
	} {
		got = got[:0]
		r := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(tc.params))).Regressions[0]
		if r.Error != nil {
			t.Fatalf("%s: fit = %s", tc.params, mustJSON(t, r.Error))
		}
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: built %#v, want %#v", tc.params, got, tc.want)
		}
	}
	if !settings[0].HMC || settings[3].HMC {
		t.Errorf("settings %+v: want hmc on the first, not on the NUTS fit", settings)
	}
}

// divergingSampler runs the sampler it wraps, then reports divergences
// and tree-depth hits on chain 0 (the latter meaningless without a tree).
type divergingSampler struct{ inner mcmc.Sampler }

func (s divergingSampler) Sample(ctx context.Context, m mcmc.Model, seed uint64, opts mcmc.Options) (mcmc.Result, error) {
	res, err := s.inner.Sample(ctx, m, seed, opts)
	if err == nil {
		res.Diagnostics.Chains[0].Divergences = 3
		res.Diagnostics.Chains[0].TreeDepthSaturated = 5
	}
	return res, err
}

// TestMMMBayes_HMCDiagnostics: an HMC fit's diagnostics omit
// treedepth_saturated and max_tree_depth and its warnings never carry
// treedepth_saturated (even when a sampler reports hits it cannot have),
// while its divergences are counted and warned with the HMC remedy; a
// NUTS fit through the same doctoring keeps both.
func TestMMMBayes_HMCDiagnostics(t *testing.T) {
	// serial: swaps the package-level MMM sampler hook (bayesSampler)

	regmmm.WrapBayesSampler(t, func(s regmmm.SamplerSettings, built mcmc.Sampler) mcmc.Sampler {
		if s.HMC {
			return divergingSampler{cheapHMC}
		}
		return divergingSampler{built}
	})
	p := bayesInstance(t, nil)
	reasons := func(r *types.RegressionResult) map[string]string {
		out := map[string]string{}
		for _, w := range r.Warnings {
			if s, ok := w.Details["reason"].(string); ok {
				out[s] = w.Message
			}
		}
		return out
	}
	hmc := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(hmcFastParams+`}`))).Regressions[0]
	if hmc.Error != nil {
		t.Fatalf("hmc fit = %s", mustJSON(t, hmc.Error))
	}
	diag := hmc.Outputs["diagnostics"].(map[string]any)
	for _, k := range []string{"treedepth_saturated", "max_tree_depth"} {
		if _, ok := diag[k]; ok {
			t.Errorf("hmc diagnostics carry %s: %s", k, mustJSON(t, diag))
		}
	}
	if diag["divergences"] != 3 {
		t.Errorf("hmc divergences = %v, want 3", diag["divergences"])
	}
	why := reasons(hmc)
	if _, ok := why[regmmm.ReasonTreeDepthSaturated]; ok {
		t.Errorf("hmc fit warns treedepth_saturated: %s", mustJSON(t, hmc.Warnings))
	}
	if msg, ok := why[regmmm.ReasonDivergences]; !ok || !strings.Contains(msg, "lower step_size") {
		t.Errorf("hmc divergence warning %q, want one naming step_size", msg)
	}

	nuts := process(t, p, bayesRequest(regmmm.BayesCohort,
		regmmm.BayesSpec(`{"channels":["tv"],"time":"week","chains":1,"warmup":20,"draws":20}`))).Regressions[0]
	if nuts.Error != nil {
		t.Fatalf("nuts fit = %s", mustJSON(t, nuts.Error))
	}
	ndiag := nuts.Outputs["diagnostics"].(map[string]any)
	if ndiag["treedepth_saturated"] != 5 || ndiag["max_tree_depth"] != mcmc.DefaultMaxTreeDepth {
		t.Errorf("nuts diagnostics %s, want treedepth_saturated 5 and max_tree_depth", mustJSON(t, ndiag))
	}
	nwhy := reasons(nuts)
	if _, ok := nwhy[regmmm.ReasonTreeDepthSaturated]; !ok {
		t.Errorf("nuts fit lacks the treedepth_saturated warning: %v", slices.Collect(func(yield func(string) bool) {
			for k := range nwhy {
				if !yield(k) {
					return
				}
			}
		}))
	}
	if msg := nwhy[regmmm.ReasonDivergences]; !strings.Contains(msg, "raise target_accept or tighten the priors") {
		t.Errorf("nuts divergence warning %q changed", msg)
	}
}
