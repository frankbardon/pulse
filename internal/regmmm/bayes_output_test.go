package regmmm

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"
)

// bayes_output_test.go holds REG_MMM_BAYES's output maths to its
// contract: summaries are R's quantile type 7 and sd(), the ROI /
// contribution / analytic mROI per draw match an independent oracle,
// and predict carries the adstock history across the holdout cutoff.
// The outputs through the engine (summaries, diagnostics, health
// warnings, the model-owned holdout) are bayes_engine_test.go's.

// TestMMMBayes_Quantile7: summaries are R's quantile(type = 7) and sd().
func TestMMMBayes_Quantile7(t *testing.T) {
	t.Parallel()

	draws := []float64{7, 1, 10, 3, 5, 2, 9, 4, 8, 6}
	s := summarize(draws, 0.9)
	// R: quantile(1:10, c(.05, .5, .95)) = 1.45 5.50 9.55; sd(1:10) = 3.02765.
	for k, want := range map[string]float64{"lower": 1.45, "median": 5.5, "upper": 9.55, "mean": 5.5, "sd": 3.0276503540974917} {
		if math.Abs(s[k]-want) > 1e-12 {
			t.Errorf("%s = %v, want %v", k, s[k], want)
		}
	}
	if s := summarize(draws, 0.5); math.Abs(s["lower"]-3.25) > 1e-12 || math.Abs(s["upper"]-7.75) > 1e-12 {
		t.Errorf("level 0.5: [%v, %v], want [3.25, 7.75]", s["lower"], s["upper"])
	}
	if s := summarize([]float64{1, math.NaN(), 3}, 0.9); !math.IsNaN(s["mean"]) || !math.IsNaN(s["lower"]) {
		t.Errorf("a NaN draw: %v, want NaN throughout", s)
	}
	if !slices.Equal(draws, []float64{7, 1, 10, 3, 5, 2, 9, 4, 8, 6}) {
		t.Error("summarize reordered its input")
	}
}

// TestMMMBayes_ChannelFiguresMatchOracle: per draw, the ROI is the
// summed contribution against zero spend over the summed spend, and the
// mROI is the central finite difference of the summed contribution
// under a uniform spend scaling, over the summed spend — an oracle that
// rebuilds the curve from the constrained parameters, in both ROI and
// β mode.
func TestMMMBayes_ChannelFiguresMatchOracle(t *testing.T) {
	t.Parallel()

	m := jointModel(t, mixedPriors())
	rng := rand.New(rand.NewPCG(5, 5^0x9E3779B97F4A7C15))
	const eps = 1e-6
	for trial := range 5 {
		theta := randomPoint(m, rng, 0.5)
		beta, roi, mroi := m.effects(theta)
		val := func(i int) float64 { return m.priors[i].transform().Forward(theta[i]).Y }
		for c := range m.nc {
			decay, k, s := val(m.iDecay(c)), val(m.iK(c)), val(m.iS(c))
			w, _ := geometricWeights(decay, m.maxLag)
			a := make([]float64, m.n)
			adstock(m.spend[c], w, nil, a, nil)
			// contribution(λ): β_raw · Σ_t hill(λ·a_t) against the
			// zero-spend counterfactual (hill(0) = 0).
			contribution := func(lambda float64) float64 {
				sum := 0.0
				for _, x := range a {
					h, _, _, _ := hillGrad(float64(lambda*x)/m.xScale[c], k, s)
					sum += h
				}
				return float64(beta[c] * sum)
			}
			if got, want := roi[c], contribution(1)/m.spendTotal[c]; math.Abs(got-want) > 1e-9*math.Abs(want) {
				t.Errorf("trial %d channel %d: roi %v, oracle %v", trial, c, got, want)
			}
			fd := (contribution(1+eps) - contribution(1-eps)) / float64(2*eps*m.spendTotal[c])
			if math.Abs(mroi[c]-fd) > float64(1e-5*math.Abs(fd))+1e-12 {
				t.Errorf("trial %d channel %d: mroi %v, finite difference %v", trial, c, mroi[c], fd)
			}
			if !(mroi[c] > 0) || mroi[c] > float64(s*roi[c]) {
				t.Errorf("trial %d channel %d: mroi %v outside (0, s·roi = %v]", trial, c, mroi[c], s*roi[c])
			}
		}
	}
}

// TestMMMBayes_PredictCarriesHistory: predict reproduces the fitted
// rows from the full columns (the adstock is causal), and a held-out
// row's prediction carries the spend before the cutoff — zeroing the
// pre-cutoff spend moves it.
func TestMMMBayes_PredictCarriesHistory(t *testing.T) {
	t.Parallel()

	y, spend, controls := jointModelData()
	const nf = 120
	cut := func(cols [][]float64) [][]float64 {
		out := make([][]float64, len(cols))
		for i, c := range cols {
			out[i] = c[:nf]
		}
		return out
	}
	m, err := newBayesModel(y[:nf], cut(spend), cut(controls), defaultMaxLag, mixedPriors())
	if err != nil {
		t.Fatal(err)
	}
	theta := randomPoint(m, rand.New(rand.NewPCG(9, 9^0x9E3779B97F4A7C15)), 0.5)
	full := m.predict(theta, spend, controls, 0)
	fitted := m.predict(theta, cut(spend), cut(controls), 0)
	if !slices.Equal(full[:nf], fitted) {
		t.Error("the full-column prediction of the fitted rows differs from the fitted columns'")
	}
	if held := m.predict(theta, spend, controls, nf); !slices.Equal(held, full[nf:]) {
		t.Error("predict from the cutoff is not the full prediction's tail")
	}
	cold := make([][]float64, len(spend))
	for c, x := range spend {
		cold[c] = slices.Clone(x)
		clear(cold[c][:nf])
	}
	if m.predict(theta, cold, controls, nf)[0] == full[nf] {
		t.Error("the first held-out row ignores the spend before the cutoff")
	}
}
