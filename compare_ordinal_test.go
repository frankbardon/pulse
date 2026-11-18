package pulse

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// compare_ordinal_test.go covers the Compose nested-model comparison of
// two REG_ORDINAL fits (U68 E5-S1, #653) — a built-in authored on the
// extension contract whose declaration carries Compare
// (extend.CompareRegression): every refusal is the same coded error at
// predict and at run, predict's summary is what the run reports, the
// comparisons answer byte for byte the same under every DecodeWorkers /
// ShardWorkers count and on Compose and ComposeParallel, and the Wald
// statistic of one tested predictor is its squared z. The figures are
// pinned to anova.polr by internal/service's
// TestReferenceOracle_NestedOrdinalMatchesR. The cohort is
// margins_test.go's; the ordinal response is its count column c.

func cmpOrdinal(levels string, preds ...string) *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_ORDINAL, Target: "c", Predictors: preds}
	if levels != "" {
		s.Params = json.RawMessage(`{"levels":` + levels + `}`)
	}
	return s
}

// cmpOrdinalValid is the valid ordinal batch: the default likelihood
// ratio, a Wald test and an hc1 Wald test.
func cmpOrdinalValid(cohort string) *ComposedRequest {
	hc1 := func(s *types.RegressionSpec) *types.RegressionSpec {
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
		return s
	}
	return cmpBatch(cohort, map[string]*types.RegressionSpec{
		"o0": cmpOrdinal("", "x1"), "o1": cmpOrdinal("", "x1", "x2"),
		"h0": hc1(cmpOrdinal("", "x1")), "h1": hc1(cmpOrdinal("", "x1", "x2")),
	},
		types.FitComparisonSpec{Name: "ord_lrt", Restricted: "o0", Full: "o1"},
		types.FitComparisonSpec{Name: "ord_wald", Restricted: "o0", Full: "o1", Test: types.ComparisonTestWald},
		types.FitComparisonSpec{Name: "ord_wald_hc1", Restricted: "h0", Full: "h1", Test: types.ComparisonTestWald},
	)
}

// TestCompare_OrdinalWorkerInvariant: the ordinal comparisons are
// byte-identical across decode workers, shard workers and Compose /
// ComposeParallel; the likelihood ratio is the deviance change on
// χ²(1); the Wald statistic of the one tested predictor is its squared
// z on the fit's own (classical or hc1) covariance.
func TestCompare_OrdinalWorkerInvariant(t *testing.T) {
	t.Parallel()

	single, sharded := marginsModes(t)
	for _, group := range [][]scorerParityMode{single, sharded} {
		var want string
		for _, m := range group {
			for name, run := range composeRuns(m.p) {
				resp, err := run(cmpOrdinalValid(m.cohort))
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
	resp, err := single[0].p.Compose(context.Background(), cmpOrdinalValid(marginsCohort))
	if err != nil {
		t.Fatal(err)
	}
	lrt, wald, hc := resp.Comparisons[0], resp.Comparisons[1], resp.Comparisons[2]
	o0, o1 := resp.Fits["o0"].RegressionResult, resp.Fits["o1"].RegressionResult
	if lrt.Test != types.ComparisonTestLRT || lrt.Distribution != types.ComparisonDistributionChiSquare || lrt.DF != 1 ||
		!slices.Equal(lrt.Terms, []string{"x2"}) || lrt.Dispersion != 1 {
		t.Fatalf("lrt %+v", lrt)
	}
	if d := o0.Deviance - o1.Deviance; math.Abs(lrt.Statistic-d) > 1e-12*math.Abs(d) || lrt.RestrictedDeviance != o0.Deviance || lrt.FullDeviance != o1.Deviance {
		t.Fatalf("lrt %v, deviance change %v", lrt.Statistic, d)
	}
	// Residual df Σw − (predictors + thresholds): polr's df.residual.
	if want := float64(o1.NObs - len(o1.Coefficients)); lrt.FullDFResidual != want {
		t.Fatalf("full residual df %v, want %v", lrt.FullDFResidual, want)
	}
	for _, c := range []struct {
		cmp types.FitComparisonResult
		fit string
	}{{wald, "o1"}, {hc, "h1"}} {
		f := resp.Fits[c.fit].RegressionResult
		z := f.Coefficients["x2"] / f.StdErrors["x2"]
		if c.cmp.Distribution != types.ComparisonDistributionChiSquare || math.Abs(c.cmp.Statistic-z*z) > 1e-9*z*z || c.cmp.DFResidual != 0 {
			t.Fatalf("%s: Wald %+v, z² %v", c.cmp.Name, c.cmp, z*z)
		}
	}
	if math.Abs(wald.Statistic-hc.Statistic) < 1e-9 {
		t.Fatalf("classical and hc1 Wald agree (%v): the robust covariance was not read", wald.Statistic)
	}
}

// TestCompare_OrdinalPredictMatchesRuntime: the ordinal refusals — an F
// test (no estimated dispersion), differing params.levels, a restricted
// predictor the full fit lacks, a mixed-type pair and a type declaring
// no comparisons — are one coded error, message and details at predict
// and on both Compose arms; predict's summary of the valid batch is what
// the run reports.
func TestCompare_OrdinalPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	pair := func(r0, r1 *types.RegressionSpec, test types.ComparisonTest) *ComposedRequest {
		return cmpBatch(marginsCohort, map[string]*types.RegressionSpec{"m0": r0, "m1": r1},
			types.FitComparisonSpec{Name: "c", Restricted: "m0", Full: "m1", Test: test})
	}
	bayes := func(preds ...string) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: preds}
	}
	for _, tc := range []struct {
		name     string
		req      *ComposedRequest
		reason   string
		requires string
	}{
		{"f", pair(cmpOrdinal("", "x1"), cmpOrdinal("", "x1", "x2"), types.ComparisonTestF), "requires", "estimated_dispersion"},
		{"levels differ", pair(cmpOrdinal(`["0","1","2","3"]`, "x1"), cmpOrdinal(`["0","1","2"]`, "x1", "x2"), ""), "levels_differ", ""},
		{"not nested", pair(cmpOrdinal("", "x2"), cmpOrdinal("", "x1", "y"), ""), "not_nested", ""},
		{"mixed types", pair(cmpOLS("x1"), cmpOrdinal("", "x1", "x2"), ""), "type_mismatch", ""},
		{"undeclared type", pair(bayes("x1"), bayes("x1", "x2"), ""), "type_unsupported", "type REG_OLS, REG_GLM, REG_MULTINOMIAL or declared_compare"},
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

	c := cmpOrdinalValid(marginsCohort)
	env, err := p.PredictCompose(ctx, c)
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict refused the valid ordinal batch: %v %+v", err, env.Errors)
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

// TestCompare_ComparableRegressions: the declared predicate predict and
// the runtime both hand regcompare.Check admits REG_ORDINAL (its
// declaration carries Compare) and no other type — REG_OLS / REG_GLM /
// REG_MULTINOMIAL are admitted by the rule itself, never through the
// predicate.
func TestCompare_ComparableRegressions(t *testing.T) {
	t.Parallel()

	inst := descx.UnscopedInstanceSnapshot(nil)
	if !descx.ComparableRegressions(inst, nil)(types.REG_ORDINAL) {
		t.Fatal("REG_ORDINAL is not comparable on an unscoped instance")
	}
	for _, typ := range []types.RegressionType{types.REG_OLS, types.REG_GLM, types.REG_MULTINOMIAL, types.REG_BAYES_LINEAR, types.REG_MMM_BAYES} {
		if descx.ComparableRegressions(inst, nil)(typ) {
			t.Errorf("%s reads as a declared-compare model", typ)
		}
	}
}

// TestCompare_OrdinalResampleKeepsDeviances: under a delete-a-group
// jackknife the base fit — never a replicate — offers the figures, so
// the likelihood ratio is byte-identical to the unresampled one.
func TestCompare_OrdinalResampleKeepsDeviances(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	single, _ := marginsModes(t)
	p := single[0].p
	jack := func(s *types.RegressionSpec) *types.RegressionSpec {
		s.Resample, s.JackknifeGroups = "jackknife", 4
		return s
	}
	run := func(r0, r1 *types.RegressionSpec) string {
		resp, err := p.Compose(ctx, cmpBatch(marginsCohort, map[string]*types.RegressionSpec{"m0": r0, "m1": r1},
			types.FitComparisonSpec{Name: "c", Restricted: "m0", Full: "m1"}))
		if err != nil {
			t.Fatal(err)
		}
		return comparisonsJSON(t, resp)
	}
	plain := run(cmpOrdinal("", "x1"), cmpOrdinal("", "x1", "x2"))
	if resampled := run(jack(cmpOrdinal("", "x1")), jack(cmpOrdinal("", "x1", "x2"))); resampled != plain {
		t.Fatalf("resampled %s, plain %s", resampled, plain)
	}
}
