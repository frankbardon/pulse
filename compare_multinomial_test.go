package pulse

import (
	"context"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// compare_multinomial_test.go covers the Compose nested-model comparison
// of two REG_MULTINOMIAL fits (U68 E5-S2, D7): admitted by the rule
// itself as a declared-like plan, every test reads the K − 1
// "<category>:<term>" coefficients of each added predictor. Every
// refusal is the same coded error at predict and at run, predict's
// summary is what the run reports, the comparisons answer byte for byte
// the same under every DecodeWorkers / ShardWorkers count and on Compose
// and ComposeParallel, and a one-predictor comparison is that
// predictor's joint term test. The figures are pinned to
// anova.multinom by internal/service's
// TestReferenceOracle_NestedMultinomialMatchesR. The cohort is
// multinomial_test.go's (target cls: mid, lo, hi).

func cmpMultinomial(ref string, preds ...string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "cls", Predictors: preds, Reference: ref, TermTests: true}
}

// cmpMultinomialValid is the valid multinomial batch: the default
// likelihood ratio, a Wald test, an hc1 Wald test and a named reference.
func cmpMultinomialValid(cohort string) *ComposedRequest {
	hc1 := func(s *types.RegressionSpec) *types.RegressionSpec {
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
		return s
	}
	return cmpBatch(cohort, map[string]*types.RegressionSpec{
		"m0": cmpMultinomial("", "x1"), "m1": cmpMultinomial("", "x1", "x2"),
		"h0": hc1(cmpMultinomial("", "x1")), "h1": hc1(cmpMultinomial("", "x1", "x2")),
		"r0": cmpMultinomial("hi", "x2"), "r1": cmpMultinomial("hi", "x2", "x1"),
	},
		types.FitComparisonSpec{Name: "mn_lrt", Restricted: "m0", Full: "m1"},
		types.FitComparisonSpec{Name: "mn_wald", Restricted: "m0", Full: "m1", Test: types.ComparisonTestWald},
		types.FitComparisonSpec{Name: "mn_wald_hc1", Restricted: "h0", Full: "h1", Test: types.ComparisonTestWald},
		types.FitComparisonSpec{Name: "mn_ref", Restricted: "r0", Full: "r1"},
	)
}

// TestCompare_MultinomialWorkerInvariant: the multinomial comparisons
// are byte-identical across decode workers, shard workers and Compose /
// ComposeParallel; with three categories one added predictor is tested
// on χ²(2); the likelihood ratio is the deviance change and the full
// fit's own LR term test, the Wald statistic its Wald term test on the
// fit's (classical or hc1) covariance; the residual df is
// (K − 1)·n − (K − 1)·(p + 1).
func TestCompare_MultinomialWorkerInvariant(t *testing.T) {
	t.Parallel()

	single, sharded := mnModes(t)
	for _, group := range [][]scorerParityMode{single, sharded} {
		var want string
		for _, m := range group {
			for name, run := range composeRuns(m.p) {
				resp, err := run(cmpMultinomialValid(m.cohort))
				if err != nil {
					t.Fatalf("%s %s: %v", m.name, name, err)
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
	resp, err := single[0].p.Compose(context.Background(), cmpMultinomialValid(mnCohort))
	if err != nil {
		t.Fatal(err)
	}
	lrt, wald, hc, ref := resp.Comparisons[0], resp.Comparisons[1], resp.Comparisons[2], resp.Comparisons[3]
	m0, m1 := resp.Fits["m0"].RegressionResult, resp.Fits["m1"].RegressionResult
	if lrt.Test != types.ComparisonTestLRT || lrt.Distribution != types.ComparisonDistributionChiSquare || lrt.DF != 2 ||
		!slices.Equal(lrt.Terms, []string{"x2"}) || lrt.Dispersion != 1 {
		t.Fatalf("lrt %+v", lrt)
	}
	if d := m0.Deviance - m1.Deviance; !(math.Abs(lrt.Statistic-d) <= 1e-12*math.Abs(d)) || lrt.RestrictedDeviance != m0.Deviance || lrt.FullDeviance != m1.Deviance {
		t.Fatalf("lrt %v, deviance change %v", lrt.Statistic, d)
	}
	if tt := m1.TermTests.LRChi2["x2"]; !(math.Abs(lrt.Statistic-tt) <= 1e-9*tt) {
		t.Fatalf("lrt %v, the full fit's LR term test %v", lrt.Statistic, tt)
	}
	// anova.multinom's Resid. df: (K − 1)·n − (K − 1)·(p + 1).
	if want := float64(2*m1.NObs - len(m1.Coefficients)); lrt.FullDFResidual != want || lrt.RestrictedDFResidual != float64(2*m0.NObs-len(m0.Coefficients)) {
		t.Fatalf("residual df %v / %v, want full %v", lrt.RestrictedDFResidual, lrt.FullDFResidual, want)
	}
	for _, c := range []struct {
		cmp types.FitComparisonResult
		fit string
	}{{wald, "m1"}, {hc, "h1"}} {
		tt := resp.Fits[c.fit].RegressionResult.TermTests.WaldChi2["x2"]
		if c.cmp.Distribution != types.ComparisonDistributionChiSquare || c.cmp.DF != 2 || !(math.Abs(c.cmp.Statistic-tt) <= 1e-9*tt) || c.cmp.DFResidual != 0 {
			t.Fatalf("%s: Wald %+v, term test %v", c.cmp.Name, c.cmp, tt)
		}
	}
	if math.Abs(wald.Statistic-hc.Statistic) < 1e-9 {
		t.Fatalf("classical and hc1 Wald agree (%v): the robust covariance was not read", wald.Statistic)
	}
	if r1 := resp.Fits["r1"].RegressionResult; r1.Reference != "hi" || ref.DF != 2 || !slices.Equal(ref.Terms, []string{"x1"}) ||
		!(math.Abs(ref.Statistic-r1.TermTests.LRChi2["x1"]) <= 1e-9*ref.Statistic) {
		t.Fatalf("named reference: %+v (fit reference %s)", ref, r1.Reference)
	}
}

// TestCompare_MultinomialPredictMatchesRuntime: the multinomial refusals
// — an F test (no estimated dispersion), differing references, a
// penalty, a restricted predictor the full fit lacks and a mixed-type
// pair — are one coded error, message and details at predict and on
// both Compose arms; predict's summary of the valid batch is what the
// run reports.
func TestCompare_MultinomialPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := mnModes(t)
	p := single[0].p
	pair := func(r0, r1 *types.RegressionSpec, test types.ComparisonTest) *ComposedRequest {
		return cmpBatch(mnCohort, map[string]*types.RegressionSpec{"m0": r0, "m1": r1},
			types.FitComparisonSpec{Name: "c", Restricted: "m0", Full: "m1", Test: test})
	}
	penalized := func(s *types.RegressionSpec) *types.RegressionSpec {
		s.Penalty, s.Alpha, s.Reference = "l2", 0.1, ""
		return s
	}
	for _, tc := range []struct {
		name     string
		req      *ComposedRequest
		reason   string
		requires string
	}{
		{"f", pair(cmpMultinomial("", "x1"), cmpMultinomial("", "x1", "x2"), types.ComparisonTestF), "requires", "estimated_dispersion"},
		{"reference differs", pair(cmpMultinomial("lo", "x1"), cmpMultinomial("hi", "x1", "x2"), ""), "reference_differs", ""},
		{"reference on one side", pair(cmpMultinomial("", "x1"), cmpMultinomial("mid", "x1", "x2"), ""), "reference_differs", ""},
		{"penalty", pair(penalized(cmpMultinomial("", "x1")), penalized(cmpMultinomial("", "x1", "x2")), ""), "requires", "unpenalized"},
		{"not nested", pair(cmpMultinomial("", "x2"), cmpMultinomial("", "x1", "f"), ""), "not_nested", ""},
		{"mixed types", pair(cmpOLS("x1"), cmpMultinomial("", "x1", "x2"), ""), "type_mismatch", ""},
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
				if resp != nil || ce.Details["reason"] != tc.reason || (tc.requires != "" && ce.Details["requires"] != tc.requires) {
					t.Fatalf("%s: details %v, want reason %s requires %q", name, ce.Details, tc.reason, tc.requires)
				}
				if env.Errors[0].Message != ce.Message || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
					t.Fatalf("%s: predict %+v differs from runtime %s %v", name, env.Errors[0], ce.Message, ce.Details)
				}
			}
		})
	}

	c := cmpMultinomialValid(mnCohort)
	env, err := p.PredictCompose(ctx, c)
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict refused the valid multinomial batch: %v %+v", err, env.Errors)
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

// TestCompare_MultinomialResampleKeepsDeviances: under a delete-a-group
// jackknife the base fit — never a replicate — offers the figures, so
// the likelihood ratio is byte-identical to the unresampled one; a Wald
// test beside resample runs on χ² over the replicate covariance (U69
// #642).
func TestCompare_MultinomialResampleKeepsDeviances(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := mnModes(t)
	p := single[0].p
	jack := func(s *types.RegressionSpec) *types.RegressionSpec {
		// term_tests reads the analytical covariance: refused beside
		// resample.
		s.Resample, s.JackknifeGroups, s.TermTests = "jackknife", 5, false
		return s
	}
	run := func(r0, r1 *types.RegressionSpec) string {
		resp, err := p.Compose(ctx, cmpBatch(mnCohort, map[string]*types.RegressionSpec{"m0": r0, "m1": r1},
			types.FitComparisonSpec{Name: "c", Restricted: "m0", Full: "m1"}))
		if err != nil {
			t.Fatal(err)
		}
		return comparisonsJSON(t, resp)
	}
	plainSpec := func(preds ...string) *types.RegressionSpec {
		s := cmpMultinomial("", preds...)
		s.TermTests = false
		return s
	}
	plain := run(plainSpec("x1"), plainSpec("x1", "x2"))
	if resampled := run(jack(cmpMultinomial("", "x1")), jack(cmpMultinomial("", "x1", "x2"))); resampled != plain {
		t.Fatalf("resampled %s, plain %s", resampled, plain)
	}
	resp, err := p.Compose(ctx, cmpBatch(mnCohort, map[string]*types.RegressionSpec{
		"m0": jack(cmpMultinomial("", "x1")), "m1": jack(cmpMultinomial("", "x1", "x2"))},
		types.FitComparisonSpec{Name: "c", Restricted: "m0", Full: "m1", Test: types.ComparisonTestWald}))
	if err != nil {
		t.Fatal(err)
	}
	if w := resp.Comparisons[0]; w.Error != nil || w.Distribution != types.ComparisonDistributionChiSquare {
		t.Fatalf("wald under resample: %+v", w)
	}
}
