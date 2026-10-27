//go:build heavy

package regmmm_test

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestMMMBayes_InputsRefused: a gap, a repeat (a panel of two markets,
// grouped request or not), a non-integer or null time and a negative
// spend are PROCESSING_CONFIG naming params.time / the channel, before
// any sampling; rows written out of time order fit exactly as the
// sorted rows do.
func TestMMMBayes_InputsRefused(t *testing.T) {
	c := &countingSampler{}
	withSampler(t, c)
	p := bayesInstance(t, nil)
	base := regmmm.BayesRows(11)
	mutate := func(f func([]regmmm.BayesRow) []regmmm.BayesRow) []regmmm.BayesRow { return f(slices.Clone(base)) }
	for _, tc := range []struct {
		name, reason string
		weekType     encoding.FieldType
		rows         []regmmm.BayesRow
		groups       bool
	}{
		{"gap", "time_gap", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { return slices.Delete(r, 20, 21) }), false},
		{"duplicate", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { r[5].Week = r[4].Week; return r }), false},
		{"panel", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow {
			for _, row := range base {
				row.Geo = "west"
				r = append(r, row)
			}
			return r
		}), false},
		{"grouped panel", "duplicate_time", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow {
			for _, row := range base {
				row.Geo = "west"
				r = append(r, row)
			}
			return r
		}), true},
		{"fractional time", "time_not_integer", encoding.FieldTypeF64, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { r[3].Week = 3.5; return r }), false},
		{"null time", "time_missing", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { r[3].Week = -1; return r }), false},
		{"negative spend", "negative_spend", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { r[7].TV = -1; return r }), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := strings.ReplaceAll(tc.name, " ", "_") + ".pulse"
			writeBayesCohort(t, p, file, tc.weekType, tc.rows)
			req := bayesRequest(file, regmmm.BayesSpec(regmmm.FastParams))
			if tc.groups {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "geo"}}
			}
			_, err := p.Process(context.Background(), req)
			var ce *perr.CodedError
			if !perr.HasCode(err, perr.PROCESSING_CONFIG) || !errorsAs(err, &ce) {
				t.Fatalf("err = %v, want PROCESSING_CONFIG", err)
			}
			if ce.Details["reason"] != tc.reason {
				t.Errorf("details = %v, want reason %s", ce.Details, tc.reason)
			}
		})
	}
	if n := c.runs.Load(); n != 0 {
		t.Errorf("a refused input sampled %d times", n)
	}

	// Out-of-order rows sort by time before the model reads them.
	writeBayesCohort(t, p, "reversed.pulse", encoding.FieldTypeU16, mutate(func(r []regmmm.BayesRow) []regmmm.BayesRow { slices.Reverse(r); return r }))
	sorted := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	reversed := process(t, p, bayesRequest("reversed.pulse", regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	if a, b := mustJSON(t, sorted.Coefficients), mustJSON(t, reversed.Coefficients); a != b || sorted.Error != nil {
		t.Errorf("reversed rows fit differently:\n%s\n%s", a, b)
	}
}

// TestMMMBayes_Seed: rng_seed 0 derives the seed from the slot's wire
// form alone (rng_seed excluded): the sampler gets it, outputs.seed
// echoes it, the same request gives the same bytes, different data
// gives the same seed, a different slot another; a non-zero rng_seed is
// used as is.
func TestMMMBayes_Seed(t *testing.T) {
	c := &countingSampler{}
	withSampler(t, c)
	p := bayesInstance(t, nil)
	writeBayesCohort(t, p, "other.pulse", encoding.FieldTypeU16, regmmm.BayesRows(12))

	s := regmmm.BayesSpec(regmmm.FastParams)
	want := regmmm.BayesEffectiveSeed(s)
	cp := *s
	cp.RNGSeed = 0
	if body, _ := json.Marshal(&cp); want == 0 || !strings.Contains(string(body), `"params"`) {
		t.Fatalf("derived seed %d over %s", want, body)
	}
	first := process(t, p, bayesRequest(regmmm.BayesCohort, s)).Regressions[0]
	second := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	if a, b := mustJSON(t, first), mustJSON(t, second); a != b {
		t.Errorf("same request, different bytes:\n%s\n%s", a, b)
	}
	if got := first.Outputs["seed"]; got != want {
		t.Errorf("outputs.seed = %v (%T), want %d", got, got, want)
	}
	other := process(t, p, bayesRequest("other.pulse", regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
	if other.Outputs["seed"] != want {
		t.Errorf("different data changed the derived seed: %v vs %d", other.Outputs["seed"], want)
	}
	if regmmm.BayesEffectiveSeed(regmmm.BayesSpec(strings.Replace(regmmm.FastParams, `"draws":100`, `"draws":101`, 1))) == want {
		t.Error("a different slot derived the same seed")
	}
	explicit := regmmm.BayesSpec(regmmm.FastParams)
	explicit.RNGSeed = 7
	if regmmm.BayesEffectiveSeed(explicit) != 7 {
		t.Error("a non-zero rng_seed is not used as is")
	}
	seeded := process(t, p, bayesRequest(regmmm.BayesCohort, explicit)).Regressions[0]
	if seeded.Outputs["seed"] != int64(7) || mustJSON(t, seeded.Coefficients) == mustJSON(t, first.Coefficients) {
		t.Errorf("rng_seed 7: outputs.seed %v, coefficients %s", seeded.Outputs["seed"], mustJSON(t, seeded.Coefficients))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seeds) != 4 || c.seeds[0] != uint64(want) || c.seeds[3] != 7 {
		t.Errorf("sampler seeds = %v, want derived %d ×3 then 7", c.seeds, uint64(want))
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
	r := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(regmmm.FastParams))).Regressions[0]
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
	for _, row := range regmmm.BayesRows(11) {
		spent += row.TV
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
		s := regmmm.BayesSpec(params)
		s.RNGSeed = 5
		return process(t, p, bayesRequest(regmmm.BayesCohort, s)).Regressions[0].Outputs["roi"].(map[string]any)["tv"].(map[string]float64)
	}
	wide, nr := level(regmmm.FastParams), level(strings.Replace(regmmm.FastParams, `"draws":100`, `"draws":100,"credible_level":0.5`, 1))
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

// TestMMMBayes_HealthWarnings: an under-sampled fit (a tiny warmup and
// few draws) reports its figures with a coded ess_low warning on the
// result and the response; each of R̂, divergences and tree-depth
// saturation warns with its own reason and figures; a healthy-looking
// run reports none of those three; under Strict the first warning
// fails the run with PROCESSING_REGRESSION_DIAGNOSTIC.
func TestMMMBayes_HealthWarnings(t *testing.T) {
	const tiny = `{"channels":["tv"],"time":"week","chains":2,"warmup":10,"draws":30}`
	p := bayesInstance(t, nil)
	resp := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(tiny)))
	r := resp.Regressions[0]
	if r.Error != nil || r.Outputs["roi"] == nil {
		t.Fatalf("under-sampled fit = %s, want its figures", mustJSON(t, r))
	}
	if got := warningReasons(t, r.Warnings); !slices.Contains(got, regmmm.ReasonESSLow) {
		t.Errorf("result warnings %v, want %s", got, regmmm.ReasonESSLow)
	}
	if got := warningReasons(t, resp.Warnings); !slices.Contains(got, regmmm.ReasonESSLow) {
		t.Errorf("response warnings %v, want %s lifted", got, regmmm.ReasonESSLow)
	}
	for _, w := range r.Warnings {
		if w.Details["reason"] == regmmm.ReasonESSLow {
			if w.Details["threshold"] != regmmm.BayesESSMin || !strings.Contains(w.Message, `"bayes"`) ||
				!slices.Contains(w.Details["parameters"].([]string), "sigma") {
				t.Errorf("ess_low warning = %s", mustJSON(t, w))
			}
		}
	}

	strict := instance(t, afero.NewMemMapFs(), func(o *pulse.Options) { o.Strict = true })
	writeBayesCohort(t, strict, regmmm.BayesCohort, encoding.FieldTypeU16, regmmm.BayesRows(11))
	if _, err := strict.Process(context.Background(), bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(tiny))); !perr.HasCode(err, perr.PROCESSING_REGRESSION_DIAGNOSTIC) {
		t.Errorf("strict under-sampled fit: %v, want PROCESSING_REGRESSION_DIAGNOSTIC", err)
	}

	// One chain: R̂ is undefined, so it cannot certify the fit — a
	// rhat_high warning with a NaN max_rhat, null on the wire.
	one := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(`{"channels":["tv"],"time":"week","chains":1,"warmup":100,"draws":60}`))).Regressions[0]
	if got := warningReasons(t, one.Warnings); !slices.Contains(got, regmmm.ReasonRHatHigh) {
		t.Errorf("one chain: reasons %v, want %s", got, regmmm.ReasonRHatHigh)
	}
	if body, err := types.MarshalFinite(one); err != nil || !strings.Contains(string(body), `"max_rhat":null`) {
		t.Errorf("one chain on the wire: %v, want max_rhat null", err)
	}

	// The sampler-run diagnostics, doctored: each reason appears exactly
	// when its figure crosses (the 200-draw fit always has a low ESS and
	// some R̂ above 1.01, but not the intercept's).
	intercept := func(ws []*types.ResponseWarning) bool {
		for _, w := range ws {
			if w.Details["reason"] == regmmm.ReasonRHatHigh && slices.Contains(w.Details["parameters"].([]string), regmmm.InterceptName) {
				return w.Details["max_rhat"].(float64) > regmmm.BayesRHatMax && w.Details["threshold"] == regmmm.BayesRHatMax
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
		{"divergences", doctoredSampler{divergences: 3}, []string{regmmm.ReasonDivergences}, false,
			func(d map[string]any) bool { return d["count"] == 3 && d["draws"] == 200 }},
		{"treedepth", doctoredSampler{saturated: 2}, []string{regmmm.ReasonTreeDepthSaturated}, false,
			func(d map[string]any) bool { return d["count"] == 2 && d["max_tree_depth"] == mcmc.DefaultMaxTreeDepth }},
		{"rhat", doctoredSampler{split: true}, nil, true, nil},
		{"baseline", doctoredSampler{}, nil, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			regmmm.SetBayesSampler(t, func(float64) mcmc.Sampler { return tc.s })
			// Pinned: the seed FastParams derived before params were
			// hashed in canonical form (E3-S7); the intercept's R̂ sits
			// under 1.01 on it, which the doctored cases rely on.
			spec := regmmm.BayesSpec(regmmm.FastParams)
			spec.RNGSeed = 8392533999830091673
			r := process(t, p, bayesRequest(regmmm.BayesCohort, spec)).Regressions[0]
			if r.Error != nil {
				t.Fatalf("fit = %s", mustJSON(t, r))
			}
			var got []string
			for _, reason := range warningReasons(t, r.Warnings) {
				if reason == regmmm.ReasonDivergences || reason == regmmm.ReasonTreeDepthSaturated {
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
		s := regmmm.BayesSpec(params)
		s.RNGSeed = 17
		return s
	}
	r := process(t, p, bayesRequest(regmmm.BayesCohort, seeded(holdout))).Regressions[0]
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
	rows := regmmm.BayesRows(11)
	writeBayesCohort(t, p, "first50.pulse", encoding.FieldTypeU16, rows[:50])
	trunc := process(t, p, bayesRequest("first50.pulse", seeded(regmmm.FastParams))).Regressions[0]
	if a, b := mustJSON(t, trunc.Coefficients), mustJSON(t, r.Coefficients); a != b {
		t.Errorf("holdout fit differs from the truncated series' fit:\n%s\n%s", b, a)
	}

	// Leakage: other outcomes past the cutoff leave the posterior alone.
	shifted := slices.Clone(rows)
	for i := 50; i < len(shifted); i++ {
		shifted[i].Sales += 40
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
		_, err := p.Process(context.Background(), bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(
			`{"channels":["tv"],"time":"week","chains":2,"warmup":150,"draws":100,"holdout_from":`+from+`}`)))
		var ce *perr.CodedError
		if !perr.HasCode(err, perr.PROCESSING_CONFIG) || !errorsAs(err, &ce) ||
			ce.Details["slot"] != "params.holdout_from" || ce.Details["reason"] != "holdout_out_of_range" {
			t.Errorf("holdout_from %s: %v, want PROCESSING_CONFIG holdout_out_of_range", from, err)
		}
	}
	_, err := p.Process(context.Background(), bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(
		`{"channels":["tv"],"time":"week","holdout_from":50.5}`)))
	if !perr.HasCode(err, perr.PROCESSING_CONFIG) {
		t.Errorf("fractional holdout_from: %v, want PROCESSING_CONFIG", err)
	}
	if n := c.runs.Load() - runs; n != 0 {
		t.Errorf("a refused holdout sampled %d times", n)
	}
	// The last week alone is a valid holdout.
	last := process(t, p, bayesRequest(regmmm.BayesCohort, regmmm.BayesSpec(strings.Replace(holdout, `51}`, `60}`, 1)))).Regressions[0]
	if lh, ok := last.Outputs["holdout"].(map[string]any); !ok || lh["holdout_rows"] != 1 || lh["fit_rows"] != 59 {
		t.Errorf("holdout_from 60 = %v", last.Outputs["holdout"])
	}
}
