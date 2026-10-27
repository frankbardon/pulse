//go:build heavy

package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestRegBayes_CredibleIntervalCoverage: simulate K independent
// datasets from y = β_true + ε with known σ, fit Bayes on each, and
// check the 95% credible interval contains the true β at least 92% of
// the time. The default prior is diffuse so the credible interval is
// well-calibrated under the data-generating process.
//
// Fixed seed for reproducibility. K = 200 keeps the test under ~1s.
func TestRegBayes_CredibleIntervalCoverage(t *testing.T) {
	const (
		K         = 200
		n         = 80
		trueSlope = 1.7
		trueInt   = 0.4
		sigma     = 0.5
	)

	// Deterministic PRNG: linear congruential generator + Box-Muller.
	// Avoid math/rand to keep the test seed truly self-contained — Go
	// versions occasionally tweak default generators.
	type lcg struct{ state uint64 }
	next := func(g *lcg) float64 {
		// Numerical Recipes constants.
		g.state = g.state*6364136223846793005 + 1442695040888963407
		// Convert top 53 bits to a uniform double in [0, 1).
		return float64(g.state>>11) / float64(1<<53)
	}
	boxMuller := func(g *lcg) float64 {
		u1 := next(g)
		if u1 < 1e-300 {
			u1 = 1e-300
		}
		u2 := next(g)
		return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	}

	g := &lcg{state: 0xDEADBEEFCAFEBABE}
	hits := 0
	for k := 0; k < K; k++ {
		records := make([]Record, n)
		for i := 0; i < n; i++ {
			x := float64(i) / float64(n) // uniform on [0, 1)
			y := trueInt + trueSlope*x + sigma*boxMuller(g)
			records[i] = mockRecord{values: map[string]float64{"x": x, "y": y}}
		}
		spec := &types.RegressionSpec{
			Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"},
		}
		eng := newBayes(t, spec)
		res, err := eng.FitBuffered(records)
		if err != nil {
			t.Fatalf("draw %d Bayes FitBuffered: %v", k, err)
		}
		ci := res.CredibleIntervals["x"]
		if ci[0] <= trueSlope && trueSlope <= ci[1] {
			hits++
		}
	}

	// Nominal coverage 95%; allow a generous margin so the test is
	// reliable across CI environments. With K = 200 the standard error
	// of the empirical coverage is ~√(0.95·0.05/200) ≈ 1.5%, so 92% is
	// ~2σ below nominal.
	if hits < 184 { // 184 / 200 = 92%
		t.Errorf("credible-interval coverage = %d/%d = %.1f%%, want ≥ 92%%",
			hits, K, 100*float64(hits)/float64(K))
	}
}
