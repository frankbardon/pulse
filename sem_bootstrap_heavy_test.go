//go:build heavy

package pulse_test

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestSEM_BootstrapProcessMatchesLavaan: a seeded semBootIters-replicate
// bootstrap through the facade — the brand-survey mediation (free and
// := rows) and the two-wave scalar-invariant fit, resampled within each
// wave — is within the B = 40 sampling tolerances of lavaan's se =
// "bootstrap" (the 1000-replicate parity is internal/sem's); the
// analytic SEs are replaced, the estimates are not.
func TestSEM_BootstrapProcessMatchesLavaan(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	t.Run("single", func(t *testing.T) {
		plain := semGroupRun(t, p, semGroupRequest(t, "brand_def", map[string]any{"group": nil, "meanstructure": true}))
		res := semGroupRun(t, p, semGroupRequest(t, "brand_def", semBootParams(map[string]any{"group": nil, "meanstructure": true})))
		checkBootProcess(t, res, loadSEMBoot(t, "brand_boot_def"), false)
		checkBootReplaced(t, plain, res)
	})
	t.Run("grouped", func(t *testing.T) {
		plain := semGroupRun(t, p, semGroupRequest(t, "brand_mgdef", map[string]any{"group_equal": "loadings,intercepts"}))
		res := semGroupRun(t, p, semGroupRequest(t, "brand_mgdef", semBootParams(map[string]any{"group_equal": "loadings,intercepts"})))
		checkBootProcess(t, res, loadSEMBoot(t, "brand_boot_mg"), true)
		checkBootReplaced(t, plain, res)
		if res.Scalars["n_group_1"] != 600 || res.Scalars["n_group_2"] != 600 {
			t.Errorf("n_group %v / %v", res.Scalars["n_group_1"], res.Scalars["n_group_2"])
		}
	})
	t.Run("covariance_only_switches_to_lavaan", func(t *testing.T) {
		res := semGroupRun(t, p, semGroupRequest(t, "brand_mgmis", map[string]any{"group": nil, "resample": "bootstrap", "bootstrap_iters": 30}))
		if res.Error != nil || len(res.Auxiliary["estimates"].ColumnKeys) != 7 {
			t.Fatalf("error %+v columns %v: a bootstrap follows lavaan's conventions", res.Error, res.Auxiliary["estimates"].ColumnKeys)
		}
	})
}

// TestSEM_BootstrapDeterministic: another seed moves the SEs, ungrouped
// and grouped. That the same seed gives the same bits — run to run,
// under every worker knob, over a shard archive — is the MAT_SEM
// invariance family's (TestSEMInvariance_Heavy, bootstrap_*).
func TestSEM_BootstrapDeterministic(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, model string
		params      map[string]any
	}{
		{"single", "brand_def", map[string]any{"group": nil}},
		{"grouped", "brand_mgdef", map[string]any{"group_equal": "loadings,intercepts"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := func(seed int) *types.Request {
				params := map[string]any{"resample": "bootstrap", "bootstrap_iters": 25, "rng_seed": seed, "ci_level": 0.9}
				for k, v := range c.params {
					params[k] = v
				}
				return semGroupRequest(t, c.model, params)
			}
			bits := func(p *pulse.Pulse, seed int) string {
				res := semGroupRun(t, p, req(seed))
				if res.Error != nil {
					t.Fatalf("error %+v", res.Error)
				}
				raw, _ := json.Marshal(res)
				return string(raw)
			}
			p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
			if bits(p, 8) == bits(p, 7) {
				t.Fatal("another seed gave the same result")
			}
		})
	}
}

// TestSEM_BootstrapDerivedSeed: without rng_seed (or at 0) the seed is
// derived from the request and the rows, REG_RESAMPLE's rule — an
// absent seed and an explicit 0 give the same bits (run to run and
// under every worker knob: TestSEMInvariance_Heavy, derived_seed_*); a
// request that differs only in the slot's name (no figure reads it)
// draws other replicates, while a pinned seed draws the same ones
// whatever the name. Twelve replicates (sem.BootstrapMinSuccesses is 10): the bits
// under test are the seed's and the row order's, whatever the count.
func TestSEM_BootstrapDerivedSeed(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, model string
		params      map[string]any
	}{
		{"single", "brand_def", map[string]any{"group": nil}},
		{"grouped", "brand_mgdef", map[string]any{"group_equal": "loadings,intercepts"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := func(seed any, name string) *types.Request {
				params := map[string]any{"resample": "bootstrap", "bootstrap_iters": 12}
				if seed != nil {
					params["rng_seed"] = seed
				}
				for k, v := range c.params {
					params[k] = v
				}
				r := semGroupRequest(t, c.model, params)
				r.Matrices[0].Name = name
				return r
			}
			ses := func(p *pulse.Pulse, r *types.Request) string {
				res := semGroupRun(t, p, r)
				if res.Error != nil {
					t.Fatalf("error %+v", res.Error)
				}
				raw, _ := json.Marshal(res.Auxiliary["estimates"])
				return string(raw)
			}
			p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
			want := ses(p, req(nil, "sem"))
			if ses(p, req(0, "sem")) != want {
				t.Fatal("an absent seed and rng_seed 0 differ")
			}
			if ses(p, req(nil, "other")) == want {
				t.Error("the derived seed does not move with the request")
			}
			if ses(p, req(11, "sem")) != ses(p, req(11, "other")) {
				t.Error("a pinned seed moved with the slot name")
			}
		})
	}
}
