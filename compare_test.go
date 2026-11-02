package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
)

// compare_test.go covers ComposedRequest.Compare (U65 E3-S3) end to end:
// every refusal is the same coded error at predict and at run
// (internal/regcompare), predict's summary names the test, distribution
// and terms the run reports, the comparisons answer byte for byte the
// same under every DecodeWorkers / ShardWorkers count and on Compose and
// ComposeParallel, a comparison moves no fit's bytes, fits on different
// rows or a failed fit fail the comparison alone (Strict fails the
// batch), and a multiplicity block corrects the comparisons in the
// batch's compose family. The figures themselves are pinned to R by
// internal/service/compose_compare_oracle_test.go. The cohort is
// margins_test.go's.

// cmpFit is a fit request over the margins cohort.
func cmpFit(cohort string, spec *types.RegressionSpec) *Request {
	return &Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{spec}}
}

// cmpPulse is an instance over the margins cohort as one file.
func cmpPulse(t *testing.T, o Options) *Pulse {
	t.Helper()
	var rows [][]string
	for i := 0; i < marginsRows; i++ {
		rows = append(rows, marginsRow(i))
	}
	o.FS = afero.NewMemMapFs()
	createTestPulseFile(t, o.FS, marginsCohort, marginsColumns, rows)
	p, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func cmpOLS(preds ...string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: preds}
}

func cmpGLM(family, target string, preds ...string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_GLM, Family: family, Target: target, Predictors: preds}
}

// cmpBatch is a batch of named fits, one counting slot and the
// comparisons.
func cmpBatch(cohort string, fits map[string]*types.RegressionSpec, compare ...types.FitComparisonSpec) *ComposedRequest {
	names := make([]string, 0, len(fits))
	for n := range fits {
		names = append(names, n)
	}
	slices.Sort(names)
	c := &ComposedRequest{
		Requests: []*Request{{Label: "rows", Cohort: &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}}}},
		Compare: compare,
	}
	for _, n := range names {
		c.Fits = append(c.Fits, types.FitSpec{Name: n, Request: cmpFit(cohort, fits[n])})
	}
	return c
}

// cmpValid is the valid batch: OLS F / lrt / HC3 Wald, logit lrt /
// Wald, poisson lrt.
func cmpValid(cohort string) *ComposedRequest {
	hc3 := func(s *types.RegressionSpec) *types.RegressionSpec {
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3}
		return s
	}
	return cmpBatch(cohort, map[string]*types.RegressionSpec{
		"ols0": cmpOLS("x1"), "ols1": cmpOLS("x1", "x2"),
		"hc0": hc3(cmpOLS("x1")), "hc1": hc3(cmpOLS("x1", "x2")),
		"logit0": cmpGLM("binomial", "b", "x1"), "logit1": cmpGLM("binomial", "b", "x1", "x2"),
		"pois0": cmpGLM("poisson", "c", "x2"), "pois1": cmpGLM("poisson", "c", "x1", "x2"),
	},
		types.FitComparisonSpec{Name: "ols_f", Restricted: "ols0", Full: "ols1"},
		types.FitComparisonSpec{Name: "ols_lrt", Restricted: "ols0", Full: "ols1", Test: types.ComparisonTestLRT},
		types.FitComparisonSpec{Name: "ols_wald_hc3", Restricted: "hc0", Full: "hc1", Test: types.ComparisonTestWald},
		types.FitComparisonSpec{Name: "logit_lrt", Restricted: "logit0", Full: "logit1"},
		types.FitComparisonSpec{Name: "logit_wald", Restricted: "logit0", Full: "logit1", Test: types.ComparisonTestWald},
		types.FitComparisonSpec{Name: "pois_lrt", Restricted: "pois0", Full: "pois1"},
	)
}

func comparisonsJSON(t *testing.T, resp *ComposedResponse) string {
	t.Helper()
	for _, c := range resp.Comparisons {
		if c.Error != nil {
			t.Fatalf("%s failed: %+v", c.Name, c.Error)
		}
	}
	b, err := json.Marshal(resp.Comparisons)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestCompare_WorkerInvariant: the comparisons are byte-identical across
// decode workers (one file), shard workers (the archive) and Compose /
// ComposeParallel; a classical OLS Wald equals the F test.
func TestCompare_WorkerInvariant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, sharded := marginsModes(t)
	for _, group := range [][]scorerParityMode{single, sharded} {
		var want string
		for _, m := range group {
			for name, run := range composeRuns(m.p) {
				resp, err := run(cmpValid(m.cohort))
				if err != nil {
					t.Fatalf("%s %s: %v", m.name, name, err)
				}
				if len(resp.Comparisons) != 6 {
					t.Fatalf("%s %s: %d comparisons", m.name, name, len(resp.Comparisons))
				}
				got := comparisonsJSON(t, resp)
				if want == "" {
					want = got
				} else if got != want {
					t.Fatalf("%s %s diverged:\n got %s\nwant %s", m.name, name, got, want)
				}
			}
		}
	}
	// The classical OLS Wald F is the anova F (one tested term: t²).
	p := single[0].p
	c := cmpBatch(marginsCohort, map[string]*types.RegressionSpec{"a": cmpOLS("x1"), "b": cmpOLS("x1", "x2")},
		types.FitComparisonSpec{Name: "f", Restricted: "a", Full: "b"},
		types.FitComparisonSpec{Name: "w", Restricted: "a", Full: "b", Test: types.ComparisonTestWald})
	resp, err := p.Compose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	f, w := resp.Comparisons[0], resp.Comparisons[1]
	tval := resp.Fits["b"].Coefficients["x2"] / resp.Fits["b"].StdErrors["x2"]
	if math.Abs(f.Statistic-w.Statistic) > 1e-9*f.Statistic || math.Abs(w.Statistic-tval*tval) > 1e-9*w.Statistic || f.DFResidual != w.DFResidual {
		t.Fatalf("F %v, Wald %v, t² %v", f.Statistic, w.Statistic, tval*tval)
	}
}

// TestCompare_ProbabilityWeightedWald: under a probability weight the
// Wald test runs (F and lrt are refused) on the full fit's own
// covariance — one tested term, so W = t² of its coefficient, on the
// coefficient table's (n_eff-based) df — and both fits keep the same
// rows.
func TestCompare_ProbabilityWeightedWald(t *testing.T) {
	t.Parallel()

	single, _ := marginsModes(t)
	p := single[0].p
	prob := types.SlotWeightOf(types.WeightSpec{Field: "id", Kind: types.WeightKindProbability})
	w0, w1 := cmpOLS("x1"), cmpOLS("x1", "x2")
	w0.Weight, w1.Weight = prob, prob
	resp, err := p.Compose(context.Background(), cmpBatch(marginsCohort, map[string]*types.RegressionSpec{"m0": w0, "m1": w1},
		types.FitComparisonSpec{Name: "w", Restricted: "m0", Full: "m1", Test: types.ComparisonTestWald}))
	if err != nil {
		t.Fatal(err)
	}
	c, full := resp.Comparisons[0], resp.Fits["m1"]
	tval := full.Coefficients["x2"] / full.StdErrors["x2"]
	if c.Error != nil || c.Distribution != types.ComparisonDistributionF || c.DFResidual != full.DF ||
		math.Abs(c.Statistic-tval*tval) > 1e-9*c.Statistic || c.NObs != full.NObs {
		t.Fatalf("comparison %+v, want F = t² = %v on df %v", c, tval*tval, full.DF)
	}
}

// TestCompare_PredictMatchesRuntime: every refusal is the same coded
// error at PredictCompose and on both Compose arms; a valid batch's
// predict summary names the test, distribution and terms each runtime
// result reports.
func TestCompare_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	prob := types.SlotWeightOf(types.WeightSpec{Field: "x2", Kind: types.WeightKindProbability})
	weighted := func(s *types.RegressionSpec) *types.RegressionSpec { s.Weight = prob; return s }
	pair := func(r0, r1 *types.RegressionSpec, test types.ComparisonTest) *ComposedRequest {
		return cmpBatch(marginsCohort, map[string]*types.RegressionSpec{"m0": r0, "m1": r1},
			types.FitComparisonSpec{Name: "c", Restricted: "m0", Full: "m1", Test: test})
	}
	for _, tc := range []struct {
		name   string
		req    *ComposedRequest
		reason string
	}{
		{"unknown fit", cmpBatch(marginsCohort, map[string]*types.RegressionSpec{"m0": cmpOLS("x1")},
			types.FitComparisonSpec{Name: "c", Restricted: "m0", Full: "m9"}), "fit_unknown"},
		{"type mismatch", pair(cmpOLS("x1"), cmpGLM("binomial", "y", "x1", "x2"), ""), "type_mismatch"},
		{"not nested", pair(cmpOLS("x1", "c"), cmpOLS("x1", "x2"), ""), "not_nested"},
		{"target differs", pair(cmpOLS("x1"), func() *types.RegressionSpec { s := cmpOLS("x1", "x2"); s.Target = "c"; return s }(), ""), "request_mismatch"},
		{"unknown test", pair(cmpOLS("x1"), cmpOLS("x1", "x2"), "score"), "test_unknown"},
		{"F on a logit", pair(cmpGLM("binomial", "b", "x1"), cmpGLM("binomial", "b", "x1", "x2"), types.ComparisonTestF), "requires"},
		{"lrt under a probability weight", pair(weighted(cmpGLM("poisson", "c", "x1")), weighted(cmpGLM("poisson", "c", "x1", "x2")), ""), "requires"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := p.PredictCompose(ctx, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_COMPARISON_INVALID) {
				t.Fatalf("predict errors %+v", env.Errors)
			}
			for name, run := range composeRuns(p) {
				resp, rerr := run(tc.req)
				ce := requireCoded(t, rerr, errors.PULSE_COMPARISON_INVALID)
				if resp != nil || ce.Details["reason"] != tc.reason {
					t.Fatalf("%s: reason %v, want %s", name, ce.Details["reason"], tc.reason)
				}
				if env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}

	// The valid batch: predict's summary is what the run reports.
	c := cmpValid(marginsCohort)
	env, err := p.PredictCompose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("predict refused the valid batch: %+v", env.Errors)
	}
	summary := env.Data.(*descx.ComposeValidationResult).Compare
	resp, err := p.Compose(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary) != len(resp.Comparisons) {
		t.Fatalf("predict %d comparisons, run %d", len(summary), len(resp.Comparisons))
	}
	for i, s := range summary {
		r := resp.Comparisons[i]
		if s.Name != r.Name || s.Test != r.Test || s.Distribution != r.Distribution || !slices.Equal(s.Terms, r.Terms) {
			t.Errorf("predict %+v, run %s %s %s %v", s, r.Name, r.Test, r.Distribution, r.Terms)
		}
	}
}

// TestCompare_AbsentIsByteIdentical: the comparisons move no fit's or
// slot's bytes (the figures ride an internal capture on the fit run,
// never the request or the result), the caller's request is untouched
// — the same bytes and the same spec values after the run — and a
// comparison-free batch carries no `comparisons` key.
func TestCompare_AbsentIsByteIdentical(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	with := cmpValid(marginsCohort)
	without := cmpValid(marginsCohort)
	without.Compare = nil
	before, _ := json.Marshal(with)
	specs := make([]types.RegressionSpec, len(with.Fits))
	for i, f := range with.Fits {
		specs[i] = *f.Request.Regressions[0]
	}
	a, err := p.Compose(ctx, with)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Compose(ctx, without)
	if err != nil {
		t.Fatal(err)
	}
	fa, _ := json.Marshal(a.Fits)
	fb, _ := json.Marshal(b.Fits)
	ra, _ := json.Marshal(a.Responses)
	rb, _ := json.Marshal(b.Responses)
	if string(fa) != string(fb) || string(ra) != string(rb) {
		t.Fatal("a comparison moved the fits' or slots' bytes")
	}
	whole, _ := json.Marshal(b)
	if strings.Contains(string(whole), `"comparisons"`) {
		t.Fatalf("comparison-free batch carries comparisons: %s", whole)
	}
	if after, _ := json.Marshal(with); string(after) != string(before) {
		t.Fatalf("Compose moved the caller's request:\n%s\n%s", before, after)
	}
	for i, f := range with.Fits {
		if !reflect.DeepEqual(*f.Request.Regressions[0], specs[i]) {
			t.Fatalf("fit %s: the caller's spec changed", f.Name)
		}
	}
}

// TestCompare_FailsAlone: fits on different rows (a full-model predictor
// null on rows the restricted fit keeps) and a fit that fails on its
// data each fail the comparison alone — its entry carries the code, a
// PULSE_SLOT_FAILED warning (slot_kind comparisons) lifts to the batch,
// every other comparison stands; Strict fails the batch with the code.
func TestCompare_FailsAlone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	attrs := []*types.Attribute{
		{Type: types.ATTR_FORMULA, Label: "x2_pos", Expression: "x1 > 0 ? x2 : nil"},
		{Type: types.ATTR_FORMULA, Label: "x1_twice", Expression: "x1 * 2"},
	}
	c := cmpBatch(marginsCohort, map[string]*types.RegressionSpec{
		"base": cmpOLS("x1"), "partial": cmpOLS("x1", "x2_pos"), "aliased": cmpOLS("x1", "x2", "x1_twice"), "full": cmpOLS("x1", "x2"),
	},
		types.FitComparisonSpec{Name: "rows", Restricted: "base", Full: "partial"},
		types.FitComparisonSpec{Name: "failed_fit", Restricted: "base", Full: "aliased"},
		types.FitComparisonSpec{Name: "fine", Restricted: "base", Full: "full"})
	for _, f := range c.Fits {
		f.Request.Attributes = attrs
	}
	for name, run := range composeRuns(p) {
		resp, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rows, failed, fine := resp.Comparisons[0], resp.Comparisons[1], resp.Comparisons[2]
		if rows.Error == nil || rows.Error.Code != string(errors.PULSE_COMPARISON_ROWS_DIFFER) {
			t.Fatalf("%s: rows %+v", name, rows.Error)
		}
		if failed.Error == nil || failed.Error.Code != string(errors.PROCESSING_REGRESSION_RANK_DEFICIENT) || failed.Error.Details["fit"] != "aliased" {
			t.Fatalf("%s: failed fit %+v", name, failed.Error)
		}
		if fine.Error != nil || !(fine.PValue >= 0) {
			t.Fatalf("%s: the standing comparison %+v", name, fine)
		}
		var kinds []string
		for _, w := range resp.Warnings {
			if w.Code == string(errors.PULSE_SLOT_FAILED) && w.Details["slot_kind"] == "comparisons" {
				kinds = append(kinds, w.Details["label"].(string))
			}
		}
		if !slices.Equal(kinds, []string{"rows", "failed_fit"}) {
			t.Fatalf("%s: comparison warnings %v (all %+v)", name, kinds, resp.Warnings)
		}
		b, _ := json.Marshal(rows)
		if strings.Contains(string(b), "statistic") {
			t.Fatalf("%s: a failed comparison carries figures: %s", name, b)
		}
	}
	strict := cmpPulse(t, Options{Strict: true})
	only := *c
	only.Compare = c.Compare[:1]
	only.Fits = c.Fits[1:] // base, full, partial (sorted after aliased)
	if _, err := strict.Compose(ctx, &only); err == nil {
		t.Fatal("Strict ran a failed comparison")
	} else {
		ce := requireCoded(t, err, errors.PULSE_COMPARISON_ROWS_DIFFER)
		if ce.Details["slot_kind"] != "comparisons" {
			t.Fatalf("strict details %v", ce.Details)
		}
	}
}

// TestCompare_Multiplicity: comparisons inherit the batch's block like a
// test and correct in its compose family (p_adjusted beside the raw p,
// which never moves); method none is byte-identical to no block; a
// family other than compose is refused alike at predict and run.
func TestCompare_Multiplicity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	plain, err := p.Compose(ctx, cmpValid(marginsCohort))
	if err != nil {
		t.Fatal(err)
	}
	holm := cmpValid(marginsCohort)
	holm.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
	resp, err := p.Compose(ctx, holm)
	if err != nil {
		t.Fatal(err)
	}
	ps := make([]float64, len(plain.Comparisons))
	for i, c := range plain.Comparisons {
		ps[i] = c.PValue
	}
	want, err := multiplicity.Adjust(multiplicity.MethodHolm, ps)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range resp.Comparisons {
		if c.PValue != ps[i] || c.PAdjusted == nil || *c.PAdjusted != want[i] || c.Multiplicity == nil ||
			c.Multiplicity.Family != types.MultiplicityFamilyCompose || c.Multiplicity.M != len(ps) || c.SignificantAdjusted == nil {
			t.Fatalf("%s: %+v, want p_adjusted %v", c.Name, c, want[i])
		}
	}
	none := cmpValid(marginsCohort)
	none.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodNone}
	nresp, err := p.Compose(ctx, none)
	if err != nil {
		t.Fatal(err)
	}
	if comparisonsJSON(t, nresp) != comparisonsJSON(t, plain) {
		t.Fatal("method none moved the comparisons")
	}
	bad := cmpValid(marginsCohort)
	bad.Compare[0].Multiplicity = &types.Multiplicity{Family: types.MultiplicityFamilyRequest}
	env, err := p.PredictCompose(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := p.Compose(ctx, bad)
	ce := requireCoded(t, rerr, errors.PULSE_MULTIPLICITY_INVALID)
	if len(env.Errors) == 0 || env.Errors[0].Code != string(ce.Code) || env.Errors[0].Message != ce.Message {
		t.Fatalf("predict %+v, run %v", env.Errors, rerr)
	}
}

// TestCompare_HiddenParity: an instance hiding capability:compose_compare
// — fits kept — refuses a set `compare` as an unknown field, and one
// hiding capability:multiplicity a comparison's own `multiplicity` (at
// compare[i]) — identically on Compose, ComposeParallel and
// PredictCompose; the instance enabling both takes the batch.
func TestCompare_HiddenParity(t *testing.T) {
	t.Parallel()

	fsys := parityFS(t)
	fitsOnly := []string{"capability:compose", "capability:compose_fits", string(types.REG_OLS), string(types.AGG_COUNT)}
	base := append(slices.Clone(fitsOnly), "capability:compose_compare")
	host := func(name string, features []string) *parityHost {
		return newParityHostWith(t, fsys, name, parityHostConfig{profiles: map[string][]string{name: features}})
	}
	ctx := context.Background()
	body := func(fits, mult string) *ComposedRequest {
		return decodeComposedJSON(t, `{"requests":[{"cohort":{"filename":"`+parityCohort+`"},"aggregations":[{"type":"AGG_COUNT","field":"age","label":"n"}]}],`+fits+
			`"compare":[{"name":"c","restricted":"a","full":"b"`+mult+`}]}`)
	}
	fit := func(name string) string {
		return `{"name":"` + name + `","request":{"cohort":{"filename":"` + parityCohort + `"},"regressions":[{"type":"REG_OLS","target":"age","predictors":["age"]}]}}`
	}
	for _, tc := range []struct {
		name     string
		hidden   *parityHost
		req      func() *ComposedRequest
		unknown  string
		location string
	}{
		{"compose_compare hidden", host("no-compare", fitsOnly),
			func() *ComposedRequest { return body(`"fits":[`+fit("a")+`,`+fit("b")+`],`, "") }, "compare", ""},
		{"multiplicity hidden", host("no-mult", base),
			func() *ComposedRequest {
				return body(`"fits":[`+fit("a")+`,`+fit("b")+`],`, `,"multiplicity":{"method":"holm"}`)
			}, "multiplicity", "compare"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.hidden.p.Compose(ctx, tc.req())
			serial := slotGateFromErr(err)
			if serial == nil || serial.code != errors.PULSE_REQUEST_UNKNOWN_FIELD || !reflect.DeepEqual(toStrings(serial.details["unknown_keys"]), []string{tc.unknown}) {
				t.Fatalf("Compose: got %+v (%v)", serial, err)
			}
			if tc.location != "" && serial.details[tc.location] != 0 {
				t.Fatalf("Compose details %v, want %s 0", serial.details, tc.location)
			}
			_, err = tc.hidden.p.ComposeParallel(ctx, tc.req(), ComposeOptions{MaxWorkers: 2})
			if par := slotGateFromErr(err); par == nil || par.code != serial.code || !reflect.DeepEqual(par.details, serial.details) {
				t.Fatalf("ComposeParallel %+v, Compose %+v", par, serial)
			}
			env, err := tc.hidden.p.PredictCompose(ctx, tc.req())
			if err != nil {
				t.Fatal(err)
			}
			if pred := slotGateFromEnv(env); pred == nil || pred.code != serial.code ||
				!reflect.DeepEqual(toStrings(pred.details["unknown_keys"]), toStrings(serial.details["unknown_keys"])) {
				t.Fatalf("PredictCompose %+v, Compose %+v", pred, serial)
			}
		})
	}
	// Enabling both, the batch passes the slot gate: what refuses it is
	// the comparison's own rule (the two fits are not nested).
	enabled := host("compare-mult", append(slices.Clone(base), "capability:multiplicity"))
	_, err := enabled.p.Compose(ctx, body(`"fits":[`+fit("a")+`,`+fit("b")+`],`, `,"multiplicity":{"method":"holm"}`))
	if ce := requireCoded(t, err, errors.PULSE_COMPARISON_INVALID); ce.Details["reason"] != "no_extra_terms" {
		t.Fatalf("enabled instance: %v", ce.Details)
	}
}

// TestCompare_ResampleKeepsDeviances: an F or likelihood-ratio
// comparison of resampled fits runs on the base fits' deviances — the
// same figures as the plain fits' — while a Wald one is refused
// (requires no_resample, follow-up #642).
func TestCompare_ResampleKeepsDeviances(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	jack := func(s *types.RegressionSpec) *types.RegressionSpec { s.Resample = "jackknife"; return s }
	run := func(r0, r1 *types.RegressionSpec, test types.ComparisonTest) (*ComposedResponse, error) {
		return p.Compose(ctx, cmpBatch(marginsCohort, map[string]*types.RegressionSpec{"m0": r0, "m1": r1},
			types.FitComparisonSpec{Name: "c", Restricted: "m0", Full: "m1", Test: test}))
	}
	for _, test := range []types.ComparisonTest{types.ComparisonTestF, types.ComparisonTestLRT} {
		plain, err := run(cmpOLS("x1"), cmpOLS("x1", "x2"), test)
		if err != nil {
			t.Fatal(err)
		}
		resampled, err := run(jack(cmpOLS("x1")), jack(cmpOLS("x1", "x2")), test)
		if err != nil {
			t.Fatal(err)
		}
		if comparisonsJSON(t, plain) != comparisonsJSON(t, resampled) {
			t.Fatalf("%s: resampled %s, plain %s", test, comparisonsJSON(t, resampled), comparisonsJSON(t, plain))
		}
	}
	_, err := run(jack(cmpOLS("x1")), jack(cmpOLS("x1", "x2")), types.ComparisonTestWald)
	if ce := requireCoded(t, err, errors.PULSE_COMPARISON_INVALID); ce.Details["requires"] != "no_resample" {
		t.Fatalf("wald under resample: %v", ce.Details)
	}
}
