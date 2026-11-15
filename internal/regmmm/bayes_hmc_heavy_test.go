//go:build heavy

package regmmm

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/mcmc"
)

// bayes_hmc_heavy_test.go validates REG_MMM_BAYES under params.sampler
// "hmc" (U70 E4-S2): static HMC (mcmc.HMC) at path_length π/2 must
// sample the same posterior NUTS does.
//
//   - Stan: two stored series of mmm_bayes_stan.json — the default
//     geometric × hill cell, and delayed × logistic with a lift test (a
//     two-parameter kernel, another curve, the lift likelihood) —
//     fitted with each case's params and seed plus sampler hmc, against
//     Stan's NUTS posterior means (stanCaseCheck) within hmcStanZ
//     combined Monte Carlo standard errors, the NUTS cases' 4.5.
//   - SBC: the geometric × hill calibration run of
//     bayes_sbc_heavy_test.go (same design, replicates, thinning and
//     verdict) with each replicate fitted by HMC.
//
// The sampler is the only thing that changes, and every kernel × curve
// density is already calibrated and cross-checked under NUTS.

// hmcPathLength is the path the validation runs: mcmc.DefaultPathLength,
// the value the skill recommends.
const hmcPathLength = mcmc.DefaultPathLength

// hmcStanZ is the HMC Stan comparison's tolerance per case, in combined
// MCSEs.
var hmcStanZ = map[string]float64{"geometric_hill": stanZ, "delayed_logistic_lift": stanZ}

// TestMMMBayes_HMCPosteriorMatchesStan: an HMC fit of each hmcStanZ
// golden series matches Stan's NUTS posterior means within hmcStanZ
// combined MCSEs.
func TestMMMBayes_HMCPosteriorMatchesStan(t *testing.T) {
	t.Parallel()

	found := 0
	for _, c := range loadStanOracle(t) {
		z, ok := hmcStanZ[c.Name]
		if !ok {
			continue
		}
		found++
		var params map[string]any
		if err := json.Unmarshal(c.Params, &params); err != nil {
			t.Fatal(err)
		}
		params["sampler"], params["path_length"] = "hmc", hmcPathLength
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		c.Params = raw
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			out := stanCaseCheck(t, c, z)
			// The fit ran static HMC: no tree, so no tree-depth figures.
			if _, nuts := out.Outputs["diagnostics"].(map[string]any)["max_tree_depth"]; nuts {
				t.Error("the hmc fit reports max_tree_depth: it did not run static HMC")
			}
		})
	}
	if found != len(hmcStanZ) {
		t.Fatalf("the golden has %d of the %d HMC cases", found, len(hmcStanZ))
	}
}

// TestMMMBayes_HMCSimulationBasedCalibration: fitted by static HMC at
// path_length π/2, every parameter's SBC rank histogram on the default
// geometric × hill cell is uniform.
func TestMMMBayes_HMCSimulationBasedCalibration(t *testing.T) {
	t.Parallel()

	sbcKernel(t, kernelByName("geometric"), curveByName("hill"), nil,
		mcmc.HMC{PathLength: hmcPathLength, TargetAccept: defaultBayesTargetAccept})
}
