package mmmpulse

import (
	"context"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_output_test.go holds REG_MMM_BAYES's outputs (E3-S3) to their
// contract: the declared Outputs and their Interpretation, every
// posterior summary finite and ordered at credible_level, the
// diagnostics block, the ROI / contribution / analytic mROI per draw
// against an independent oracle, the coded sampler-health warnings
// (lifted, Strict-escalated), and the model-owned holdout (the
// likelihood blind to the held-out rows, the adstock over the full
// history, PROCESSING_CONFIG out of range).

// summaryKeys is every posterior summary's key set.
var summaryKeys = []string{"lower", "mean", "median", "sd", "upper"}

// checkSummary fails unless s is a finite, ordered summary.
func checkSummary(t *testing.T, what string, s any) map[string]float64 {
	t.Helper()
	m, ok := s.(map[string]float64)
	if !ok {
		t.Fatalf("%s = %T %v, want a summary", what, s, s)
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		keys = append(keys, k)
		if !finite(v) {
			t.Errorf("%s.%s = %v, want finite", what, k, v)
		}
	}
	slices.Sort(keys)
	if !slices.Equal(keys, summaryKeys) {
		t.Errorf("%s keys = %v, want %v", what, keys, summaryKeys)
	}
	if !(m["lower"] <= m["median"] && m["median"] <= m["upper"] && m["lower"] < m["upper"]) || !(m["sd"] > 0) {
		t.Errorf("%s = %v: want lower ≤ median ≤ upper, lower < upper, sd > 0", what, m)
	}
	return m
}

// TestMMMBayes_OutputSchemaInterpreted: every declared output key has
// an Interpretation, every Interpretation reads a declared key or the
// coefficient table, and the registration's Params list holdout_from.
func TestMMMBayes_OutputSchemaInterpreted(t *testing.T) {
	reg := BayesRegistration()
	var keys []string
	for _, o := range reg.OutputSchema {
		keys = append(keys, o.Key)
	}
	want := []string{"seed", "roi", "mroi", "contribution", "params", "diagnostics", "holdout"}
	if !slices.Equal(keys, want) {
		t.Errorf("OutputSchema = %v, want %v", keys, want)
	}
	read := map[string]bool{}
	for _, in := range reg.Interpretation {
		read[in.Field] = true
		if k, ok := strings.CutPrefix(in.Field, "outputs."); ok && !slices.Contains(keys, k) {
			t.Errorf("Interpretation reads undeclared %s", in.Field)
		}
	}
	for _, k := range keys {
		if !read["outputs."+k] {
			t.Errorf("output %s has no Interpretation", k)
		}
	}
	if !read["coefficients.*"] {
		t.Error("the coefficient table has no Interpretation")
	}
}

// TestMMMBayes_Quantile7: summaries are R's quantile(type = 7) and sd().
func TestMMMBayes_Quantile7(t *testing.T) {
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

// TestMMMBayes_FitOutputs: a fit's Outputs carry finite, ordered
// summaries of every channel's ROI, mROI and contribution and every
// coefficient, at credible_level; the params means are the coefficient
// table's; the contribution is the ROI times the summed spend; a lower
// credible_level narrows every interval; the diagnostics block reports
// the run; no holdout without params.holdout_from.
func TestMMMBayes_FitOutputs(t *testing.T) {
	p := bayesInstance(t, nil)
	r := process(t, p, bayesRequest(bayesCohort, bayesSpec(fastParams))).Regressions[0]
	if r.Error != nil {
		t.Fatalf("fit = %s", mustJSON(t, r))
	}
	out := r.Outputs
	for _, k := range []string{"seed", "roi", "mroi", "contribution", "params", "diagnostics"} {
		if _, ok := out[k]; !ok {
			t.Errorf("outputs lacks %s", k)
		}
	}
	if _, ok := out["holdout"]; ok {
		t.Error("outputs.holdout present without params.holdout_from")
	}
	roi := checkSummary(t, "roi.tv", out["roi"].(map[string]any)["tv"])
	checkSummary(t, "mroi.tv", out["mroi"].(map[string]any)["tv"])
	contrib := checkSummary(t, "contribution.tv", out["contribution"].(map[string]any)["tv"])
	spent := 0.0
	for _, row := range bayesRows(11) {
		spent += row.tv
	}
	if want := float64(roi["mean"] * spent); math.Abs(contrib["mean"]-want) > float64(1e-9*want) {
		t.Errorf("contribution mean %v, roi mean × spend %v", contrib["mean"], want)
	}
	params := out["params"].(map[string]any)
	if len(params) != len(r.Coefficients) {
		t.Errorf("params has %d entries, the coefficient table %d", len(params), len(r.Coefficients))
	}
	for name, est := range r.Coefficients {
		s := checkSummary(t, "params."+name, params[name])
		if s["mean"] != est {
			t.Errorf("params.%s.mean %v, coefficient %v", name, s["mean"], est)
		}
		if math.Abs(s["sd"]-r.StdErrors[name]) > 1e-9*s["sd"] {
			t.Errorf("params.%s.sd %v, std error %v", name, s["sd"], r.StdErrors[name])
		}
	}

	// The same draws (an explicit rng_seed: credible_level is part of
	// the slot a derived seed hashes) at two levels.
	level := func(params string) map[string]float64 {
		s := bayesSpec(params)
		s.RNGSeed = 5
		return process(t, p, bayesRequest(bayesCohort, s)).Regressions[0].Outputs["roi"].(map[string]any)["tv"].(map[string]float64)
	}
	wide, nr := level(fastParams), level(strings.Replace(fastParams, `"draws":100`, `"draws":100,"credible_level":0.5`, 1))
	if nr["mean"] != wide["mean"] || nr["median"] != wide["median"] || !(nr["upper"]-nr["lower"] < wide["upper"]-wide["lower"]) {
		t.Errorf("credible_level 0.5 roi %v vs 0.9 %v: want the same draws, a narrower interval", nr, wide)
	}

	d := out["diagnostics"].(map[string]any)
	for k, want := range map[string]any{"chains": 2, "warmup": 150, "draws": 100, "max_tree_depth": mcmc.DefaultMaxTreeDepth} {
		if d[k] != want {
			t.Errorf("diagnostics.%s = %v, want %v", k, d[k], want)
		}
	}
	if steps := d["step_size"].([]float64); len(steps) != 2 || !(steps[0] > 0) {
		t.Errorf("diagnostics.step_size = %v", steps)
	}
	if _, ok := d["map_start"].(bool); !ok {
		t.Errorf("diagnostics.map_start = %v", d["map_start"])
	}
	for _, k := range []string{"divergences", "treedepth_saturated"} {
		if n, ok := d[k].(int); !ok || n < 0 {
			t.Errorf("diagnostics.%s = %v", k, d[k])
		}
	}
	conv := d["parameters"].(map[string]any)
	maxRHat := 0.0
	for name := range r.Coefficients {
		c, ok := conv[name].(map[string]float64)
		if !ok || !(c["rhat"] > 0.9) || !(c["ess_bulk"] > 0) || !(c["ess_tail"] > 0) {
			t.Errorf("diagnostics.parameters.%s = %v", name, conv[name])
		}
		maxRHat = max(maxRHat, c["rhat"])
	}
	if c, ok := d["roi"].(map[string]any)["tv"].(map[string]float64); !ok || !(c["rhat"] > 0.9) {
		t.Errorf("diagnostics.roi.tv = %v", d["roi"])
	}
	if d["max_rhat"].(float64) < maxRHat {
		t.Errorf("max_rhat %v below a parameter's %v", d["max_rhat"], maxRHat)
	}
}

// doctoredSampler runs NUTS, then marks divergences and tree-depth hits
// on chain 0 and, when split, shifts one chain of the intercept's θ so
// the chains disagree.
type doctoredSampler struct {
	divergences, saturated int
	split                  bool
}

func (s doctoredSampler) Sample(ctx context.Context, m mcmc.Model, seed uint64, opts mcmc.Options) (mcmc.Result, error) {
	res, err := mcmc.NUTS{}.Sample(ctx, m, seed, opts)
	if err != nil {
		return res, err
	}
	res.Diagnostics.Chains[0].Divergences = s.divergences
	res.Diagnostics.Chains[0].TreeDepthSaturated = s.saturated
	if s.split {
		for d := range res.Draws[1][0] {
			res.Draws[1][0][d] += 5
		}
	}
	return res, nil
}

// warningReasons is the PROCESSING_REGRESSION_DIAGNOSTIC reasons in ws.
func warningReasons(t *testing.T, ws []*types.ResponseWarning) []string {
	t.Helper()
	var out []string
	for _, w := range ws {
		if w.Code != string(perr.PROCESSING_REGRESSION_DIAGNOSTIC) {
			continue
		}
		reason, _ := w.Details["reason"].(string)
		out = append(out, reason)
	}
	return out
}

// TestMMMBayes_HealthWarnings: an under-sampled fit (a tiny warmup and
// few draws) reports its figures with a coded ess_low warning on the
// result and the response; each of R̂, divergences and tree-depth
// saturation warns with its own reason and figures; a healthy-looking
// run reports none of those three; under Strict the first warning
// fails the run with PROCESSING_REGRESSION_DIAGNOSTIC.
func TestMMMBayes_HealthWarnings(t *testing.T) {
	const tiny = `{"channels":["tv"],"time":"week","chains":2,"warmup":10,"draws":30}`
	p := bayesInstance(t, nil)
	resp := process(t, p, bayesRequest(bayesCohort, bayesSpec(tiny)))
	r := resp.Regressions[0]
	if r.Error != nil || r.Outputs["roi"] == nil {
		t.Fatalf("under-sampled fit = %s, want its figures", mustJSON(t, r))
	}
	if got := warningReasons(t, r.Warnings); !slices.Contains(got, reasonESSLow) {
		t.Errorf("result warnings %v, want %s", got, reasonESSLow)
	}
	if got := warningReasons(t, resp.Warnings); !slices.Contains(got, reasonESSLow) {
		t.Errorf("response warnings %v, want %s lifted", got, reasonESSLow)
	}
	for _, w := range r.Warnings {
		if w.Details["reason"] == reasonESSLow {
			if w.Details["threshold"] != bayesESSMin || !strings.Contains(w.Message, `"bayes"`) ||
				!slices.Contains(w.Details["parameters"].([]string), "sigma") {
				t.Errorf("ess_low warning = %s", mustJSON(t, w))
			}
		}
	}

	strict := instance(t, afero.NewMemMapFs(), func(o *pulse.Options) { o.Strict = true })
	writeBayesCohort(t, strict, bayesCohort, encoding.FieldTypeU16, bayesRows(11))
	if _, err := strict.Process(context.Background(), bayesRequest(bayesCohort, bayesSpec(tiny))); !perr.HasCode(err, perr.PROCESSING_REGRESSION_DIAGNOSTIC) {
		t.Errorf("strict under-sampled fit: %v, want PROCESSING_REGRESSION_DIAGNOSTIC", err)
	}

	// One chain: R̂ is undefined, so it cannot certify the fit — a
	// rhat_high warning with a NaN max_rhat, null on the wire.
	one := process(t, p, bayesRequest(bayesCohort, bayesSpec(`{"channels":["tv"],"time":"week","chains":1,"warmup":100,"draws":60}`))).Regressions[0]
	if got := warningReasons(t, one.Warnings); !slices.Contains(got, reasonRHatHigh) {
		t.Errorf("one chain: reasons %v, want %s", got, reasonRHatHigh)
	}
	if body, err := types.MarshalFinite(one); err != nil || !strings.Contains(string(body), `"max_rhat":null`) {
		t.Errorf("one chain on the wire: %v, want max_rhat null", err)
	}

	// The sampler-run diagnostics, doctored: each reason appears exactly
	// when its figure crosses (the 200-draw fit always has a low ESS and
	// some R̂ above 1.01, but not the intercept's).
	intercept := func(ws []*types.ResponseWarning) bool {
		for _, w := range ws {
			if w.Details["reason"] == reasonRHatHigh && slices.Contains(w.Details["parameters"].([]string), interceptName) {
				return w.Details["max_rhat"].(float64) > bayesRHatMax && w.Details["threshold"] == bayesRHatMax
			}
		}
		return false
	}
	for _, tc := range []struct {
		name      string
		s         doctoredSampler
		reasons   []string
		intercept bool
		check     func(map[string]any) bool
	}{
		{"divergences", doctoredSampler{divergences: 3}, []string{reasonDivergences}, false,
			func(d map[string]any) bool { return d["count"] == 3 && d["draws"] == 200 }},
		{"treedepth", doctoredSampler{saturated: 2}, []string{reasonTreeDepthSaturated}, false,
			func(d map[string]any) bool { return d["count"] == 2 && d["max_tree_depth"] == mcmc.DefaultMaxTreeDepth }},
		{"rhat", doctoredSampler{split: true}, nil, true, nil},
		{"baseline", doctoredSampler{}, nil, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev := bayesSampler
			bayesSampler = func(float64) mcmc.Sampler { return tc.s }
			t.Cleanup(func() { bayesSampler = prev })
			r := process(t, p, bayesRequest(bayesCohort, bayesSpec(fastParams))).Regressions[0]
			if r.Error != nil {
				t.Fatalf("fit = %s", mustJSON(t, r))
			}
			var got []string
			for _, reason := range warningReasons(t, r.Warnings) {
				if reason == reasonDivergences || reason == reasonTreeDepthSaturated {
					got = append(got, reason)
				}
			}
			if !slices.Equal(got, tc.reasons) {
				t.Fatalf("reasons = %v, want %v", got, tc.reasons)
			}
			if intercept(r.Warnings) != tc.intercept {
				t.Errorf("rhat_high on the intercept = %v, want %v: %s", !tc.intercept, tc.intercept, mustJSON(t, r.Warnings))
			}
			for _, w := range r.Warnings {
				if tc.check != nil && w.Details["reason"] == tc.reasons[0] && !tc.check(w.Details) {
					t.Errorf("warning = %s", mustJSON(t, w))
				}
			}
		})
	}
}

// TestMMMBayes_Holdout: params.holdout_from fits the rows before it and
// scores the rest: outputs.holdout reports the split, a finite RMSE and
// a coverage share; the fit is the fit of the truncated series (same
// rng_seed, same coefficient bytes); changing the held-out outcomes
// leaves the posterior untouched and moves only the scores; a cutoff
// outside the series is PROCESSING_CONFIG before any sampling, a
// fractional one a params refusal.
func TestMMMBayes_Holdout(t *testing.T) {
	c := &countingSampler{}
	withSampler(t, c)
	p := bayesInstance(t, nil)
	const holdout = `{"channels":["tv"],"time":"week","chains":2,"warmup":150,"draws":100,"holdout_from":51}`
	seeded := func(params string) *types.RegressionSpec {
		s := bayesSpec(params)
		s.RNGSeed = 17
		return s
	}
	r := process(t, p, bayesRequest(bayesCohort, seeded(holdout))).Regressions[0]
	if r.Error != nil {
		t.Fatalf("holdout fit = %s", mustJSON(t, r))
	}
	h, ok := r.Outputs["holdout"].(map[string]any)
	if !ok {
		t.Fatalf("outputs.holdout = %v", r.Outputs["holdout"])
	}
	if h["from"] != int64(51) || h["fit_rows"] != 50 || h["holdout_rows"] != 10 || h["credible_level"] != 0.9 {
		t.Errorf("holdout split = %v", h)
	}
	rmse, cov := h["rmse"].(float64), h["coverage"].(float64)
	if !(rmse > 0 && rmse < 10) || !(cov >= 0 && cov <= 1) || float64(cov*10) != math.Round(float64(cov*10)) {
		t.Errorf("holdout rmse %v coverage %v", rmse, cov)
	}

	// The likelihood is the first 50 weeks': the truncated cohort fits
	// to the same bytes.
	rows := bayesRows(11)
	writeBayesCohort(t, p, "first50.pulse", encoding.FieldTypeU16, rows[:50])
	trunc := process(t, p, bayesRequest("first50.pulse", seeded(fastParams))).Regressions[0]
	if a, b := mustJSON(t, trunc.Coefficients), mustJSON(t, r.Coefficients); a != b {
		t.Errorf("holdout fit differs from the truncated series' fit:\n%s\n%s", b, a)
	}

	// Leakage: other outcomes past the cutoff leave the posterior alone.
	shifted := slices.Clone(rows)
	for i := 50; i < len(shifted); i++ {
		shifted[i].sales += 40
	}
	writeBayesCohort(t, p, "shifted.pulse", encoding.FieldTypeU16, shifted)
	s := process(t, p, bayesRequest("shifted.pulse", seeded(holdout))).Regressions[0]
	for _, k := range []string{"roi", "mroi", "contribution", "params", "diagnostics", "seed"} {
		if a, b := mustJSON(t, s.Outputs[k]), mustJSON(t, r.Outputs[k]); a != b {
			t.Errorf("outputs.%s moved with the held-out outcomes", k)
		}
	}
	if mustJSON(t, s.Coefficients) != mustJSON(t, r.Coefficients) {
		t.Error("coefficients moved with the held-out outcomes")
	}
	if sh := s.Outputs["holdout"].(map[string]any); !(sh["rmse"].(float64) > rmse+20) {
		t.Errorf("shifted holdout rmse %v, unshifted %v: want the scores to see the shift", sh["rmse"], rmse)
	}

	runs := c.runs.Load()
	for _, from := range []string{"1", "0", "61", "-3"} {
		_, err := p.Process(context.Background(), bayesRequest(bayesCohort, bayesSpec(
			`{"channels":["tv"],"time":"week","chains":2,"warmup":150,"draws":100,"holdout_from":`+from+`}`)))
		var ce *perr.CodedError
		if !perr.HasCode(err, perr.PROCESSING_CONFIG) || !errorsAs(err, &ce) ||
			ce.Details["slot"] != "params.holdout_from" || ce.Details["reason"] != "holdout_out_of_range" {
			t.Errorf("holdout_from %s: %v, want PROCESSING_CONFIG holdout_out_of_range", from, err)
		}
	}
	_, err := p.Process(context.Background(), bayesRequest(bayesCohort, bayesSpec(
		`{"channels":["tv"],"time":"week","holdout_from":50.5}`)))
	if !perr.HasCode(err, perr.PROCESSING_CONFIG) {
		t.Errorf("fractional holdout_from: %v, want PROCESSING_CONFIG", err)
	}
	if n := c.runs.Load() - runs; n != 0 {
		t.Errorf("a refused holdout sampled %d times", n)
	}
	// The last week alone is a valid holdout.
	last := process(t, p, bayesRequest(bayesCohort, bayesSpec(strings.Replace(holdout, `51}`, `60}`, 1)))).Regressions[0]
	if lh, ok := last.Outputs["holdout"].(map[string]any); !ok || lh["holdout_rows"] != 1 || lh["fit_rows"] != 59 {
		t.Errorf("holdout_from 60 = %v", last.Outputs["holdout"])
	}
}
