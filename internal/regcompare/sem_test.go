package regcompare

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// semReq is a SEM fit request over a, b, c, d with params p.
func semReq(p map[string]any) *types.Request {
	raw, _ := json.Marshal(p)
	return &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"},
		Matrices: []types.MatrixSpec{{Name: "s", Type: types.MAT_SEM, Fields: []string{"a", "b", "c", "d"}, Params: raw}}}
}

func semFits(p0, p1 map[string]any) []types.FitSpec {
	return []types.FitSpec{{Name: "m0", Request: semReq(p0)}, {Name: "m1", Request: semReq(p1)}}
}

const semTwo = "f =~ a + b\ng =~ c + d"

// TestCheckSEM_Rules: the static SEM-pair rules — each refusal's
// reason, and a valid pair's plan (lrt on chi-square, no terms, scaled
// on WLSMV only) under the neutral knobs and the normalised model view.
func TestCheckSEM_Rules(t *testing.T) {
	t.Parallel()

	restricted := map[string]any{"model": semTwo + "\nf ~~ 0*g"}
	full := map[string]any{"model": semTwo}
	for _, tc := range []struct {
		name   string
		fits   []types.FitSpec
		test   types.ComparisonTest
		reason string
	}{
		{"kind mismatch", []types.FitSpec{{Name: "m0", Request: semReq(restricted)}, {Name: "m1", Request: fitReq(ols("a"))}}, "", ReasonKindMismatch},
		{"kind mismatch, regression first", []types.FitSpec{{Name: "m0", Request: fitReq(ols("a"))}, {Name: "m1", Request: semReq(full)}}, "", ReasonKindMismatch},
		{"f", semFits(restricted, full), types.ComparisonTestF, ReasonTestUnsupported},
		{"wald", semFits(restricted, full), types.ComparisonTestWald, ReasonTestUnsupported},
		{"unknown", semFits(restricted, full), "score", "test_unknown"},
		{"estimator", semFits(restricted, map[string]any{"model": semTwo, "estimator": "dwls", "ordered": []string{"a"}}), "", "request_mismatch"},
		{"group", semFits(restricted, map[string]any{"model": semTwo, "group": "w"}), "", "request_mismatch"},
		{"meanstructure", semFits(restricted, map[string]any{"model": semTwo, "meanstructure": true}), "", "request_mismatch"},
		{"same model", semFits(full, map[string]any{"model": "  " + semTwo + "\n", "solver": "slsqp", "ci_level": 0.9}), "", ReasonSameModel},
		{"same invariance set", semFits(
			map[string]any{"model": semTwo, "group": "w", "group_equal": "intercepts, loadings"},
			map[string]any{"model": semTwo, "group": "w", "group_equal": []string{"loadings", "intercepts"}}), "", ReasonSameModel},
		{"observed differ", semFits(map[string]any{"model": "f =~ a + b + c"}, full), "", ReasonObservedDiffer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Check([]types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "m1", Test: tc.test}}, tc.fits, nil, nil)
			if err == nil || err.Code != errors.PULSE_COMPARISON_INVALID || err.Details["reason"] != tc.reason {
				t.Fatalf("got %v, want reason %s", err, tc.reason)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		p0, p1 map[string]any
		scaled bool
	}{
		{"ml", restricted, full, false},
		{"explicit ml beside the default", map[string]any{"model": semTwo + "\nf ~~ 0*g", "estimator": "ml", "meanstructure": false}, full, false},
		{"invariance step", map[string]any{"model": semTwo, "group": "w", "group_equal": []string{"loadings"}},
			map[string]any{"model": semTwo, "group": "w", "bootstrap_iters": 9}, false},
		{"wlsmv", map[string]any{"model": semTwo + "\nf ~~ 0*g", "estimator": "wlsmv", "ordered": []string{"a"}},
			map[string]any{"model": semTwo, "estimator": "wlsmv", "ordered": []string{"a"}}, true},
		{"dwls", map[string]any{"model": semTwo + "\nf ~~ 0*g", "estimator": "dwls", "ordered": []string{"a"}},
			map[string]any{"model": semTwo, "estimator": "dwls", "ordered": []string{"a"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plans, err := Check([]types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "m1"}}, semFits(tc.p0, tc.p1), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			p := plans[0]
			if !p.SEM || p.Scaled != tc.scaled || p.Test != types.ComparisonTestLRT || p.Distribution != types.ComparisonDistributionChiSquare || p.Terms == nil || len(p.Terms) != 0 {
				t.Fatalf("plan %+v", p)
			}
		})
	}
}

// TestCheck_SEMFitsListed: an unknown name lists the SEM fits beside
// the regression fits under details.available.
func TestCheck_SEMFitsListed(t *testing.T) {
	t.Parallel()

	fits := append(semFits(map[string]any{"model": semTwo}, map[string]any{"model": semTwo}), types.FitSpec{Name: "r", Request: fitReq(ols("a"))})
	_, err := Check([]types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "mx"}}, fits, nil, nil)
	if err == nil || err.Details["reason"] != "fit_unknown" {
		t.Fatalf("got %v", err)
	}
	if got, _ := err.Details["available"].([]string); len(got) != 3 || got[0] != "m0" || got[2] != "r" {
		t.Fatalf("available %v", err.Details["available"])
	}
}

func semFitOf(chi2, dof, trace float64) *model.SEMFit {
	return &model.SEMFit{Chi2: chi2, DoF: dof, TraceUG: trace, N: 100, SumWeights: 100, Lavaan: true, Means: true,
		Observed: []string{"a", "b"}, Groups: []model.SEMGroup{{N: 100, SumWeights: 100}}}
}

// TestComputeSEM_HandComputed: the plain and the Satorra–Bentler (2001)
// scaled difference against the closed forms — c₁ = 1 on a saturated
// full fit, 0 on a perfect one, a negative c_d undefined (statistic,
// p and the factor null) — and every fails-alone outcome.
func TestComputeSEM_HandComputed(t *testing.T) {
	t.Parallel()

	plan := Plan{Index: 0, Name: "c", Restricted: "m0", Full: "m1", Test: types.ComparisonTestLRT,
		Distribution: types.ComparisonDistributionChiSquare, Terms: []string{}, SEM: true}
	scaled := plan
	scaled.Scaled = true
	ok := &types.SEMFitResult{}
	for _, tc := range []struct {
		name     string
		p        Plan
		s0, s1   *model.SEMFit
		stat, cd float64
	}{
		{"plain", plan, semFitOf(30, 10, 0), semFitOf(12, 7, 0), 18, 0},
		{"scaled", scaled, semFitOf(30, 10, 12), semFitOf(12, 7, 7.7), 18 / ((12 - 7.7) / 3), (12 - 7.7) / 3},
		{"saturated full", scaled, semFitOf(30, 10, 12), semFitOf(1e-6, 0, 0), (30 - 1e-6) / 1.2, 1.2},
		{"perfect full", scaled, semFitOf(30, 10, 12), semFitOf(1e-9, 7, 7.7), (30 - 1e-9) / 4, 4},
		{"negative factor", scaled, semFitOf(30, 10, 5), semFitOf(12, 7, 7.7), math.NaN(), math.NaN()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := ComputeSEM(tc.p, ok, ok, tc.s0, tc.s1)
			if err != nil || r.Error != nil {
				t.Fatalf("%v %+v", err, r.Error)
			}
			same := func(a, b float64) bool {
				return (math.IsNaN(a) && math.IsNaN(b)) || math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b))
			}
			if !same(r.Statistic, tc.stat) || !same(r.ScalingFactor, tc.cd) {
				t.Fatalf("statistic %v scaling %v, want %v %v", r.Statistic, r.ScalingFactor, tc.stat, tc.cd)
			}
			want := chiSquareP(tc.stat, r.DF)
			if !same(r.PValue, want) || r.DF != tc.s0.DoF-tc.s1.DoF || r.RestrictedDeviance != tc.s0.Chi2 || r.FullDeviance != tc.s1.Chi2 ||
				r.RestrictedDFResidual != tc.s0.DoF || r.FullDFResidual != tc.s1.DoF || r.NObs != 100 {
				t.Fatalf("result %+v", r)
			}
		})
	}

	failed := &types.SEMFitResult{Error: &types.SlotError{Code: "PULSE_MATRIX_SEM_NOT_CONVERGED", Message: "no"}}
	for _, tc := range []struct {
		name         string
		r0           *types.SEMFitResult
		s0, s1       *model.SEMFit
		code, reason string
	}{
		{"failed fit", failed, nil, semFitOf(12, 7, 0), "PULSE_MATRIX_SEM_NOT_CONVERGED", ""},
		{"df equal", ok, semFitOf(30, 7, 0), semFitOf(12, 7, 0), string(errors.PULSE_COMPARISON_INVALID), ReasonDFNotPositive},
		{"swapped", ok, semFitOf(12, 7, 0), semFitOf(30, 10, 0), string(errors.PULSE_COMPARISON_INVALID), ReasonDFNotPositive},
		{"conventions", ok, func() *model.SEMFit { f := semFitOf(30, 10, 0); f.Lavaan = false; return f }(), semFitOf(12, 7, 0),
			string(errors.PULSE_COMPARISON_INVALID), ReasonConventionsDiffer},
		{"means", ok, func() *model.SEMFit { f := semFitOf(30, 10, 0); f.Means = false; return f }(), semFitOf(12, 7, 0),
			string(errors.PULSE_COMPARISON_INVALID), ReasonConventionsDiffer},
		{"rows", ok, func() *model.SEMFit { f := semFitOf(30, 10, 0); f.N = 99; return f }(), semFitOf(12, 7, 0),
			string(errors.PULSE_COMPARISON_ROWS_DIFFER), "rows_differ"},
		{"groups", ok, func() *model.SEMFit {
			f := semFitOf(30, 10, 0)
			f.Groups = []model.SEMGroup{{Level: "1", N: 60, SumWeights: 60}, {Level: "2", N: 40, SumWeights: 40}}
			return f
		}(), semFitOf(12, 7, 0), string(errors.PULSE_COMPARISON_ROWS_DIFFER), "rows_differ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := ComputeSEM(plan, tc.r0, ok, tc.s0, tc.s1)
			if err != nil {
				t.Fatal(err)
			}
			if r.Error == nil || r.Error.Code != tc.code || (tc.reason != "" && r.Error.Details["reason"] != tc.reason) {
				t.Fatalf("error %+v, want %s %s", r.Error, tc.code, tc.reason)
			}
			if tc.reason == "" && r.Error.Details["fit"] != "m0" {
				t.Fatalf("details %v", r.Error.Details)
			}
		})
	}

	// A fitted fit without its figures is an invariant violation.
	if _, err := ComputeSEM(plan, ok, ok, semFitOf(30, 10, 0), nil); err == nil {
		t.Fatal("no figures accepted")
	}
}
