package regcompare

import (
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

func fitReq(spec *types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Regressions: []*types.RegressionSpec{spec}}
}

func ols(preds ...string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: preds}
}

func glm(family string, preds ...string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_GLM, Family: family, Target: "y", Predictors: preds}
}

// pair is the fits list {m0: r0, m1: r1}.
func pair(r0, r1 *types.RegressionSpec) []types.FitSpec {
	return []types.FitSpec{{Name: "m0", Request: fitReq(r0)}, {Name: "m1", Request: fitReq(r1)}}
}

func one(test types.ComparisonTest) []types.FitComparisonSpec {
	return []types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "m1", Test: test}}
}

// TestCheck_Refusals: every rule refuses with PULSE_COMPARISON_INVALID,
// its reason (and requires), located by details.compare.
func TestCheck_Refusals(t *testing.T) {
	t.Parallel()

	prob := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	with := func(s *types.RegressionSpec, mut func(*types.RegressionSpec)) *types.RegressionSpec {
		mut(s)
		return s
	}
	for _, tc := range []struct {
		name     string
		compare  []types.FitComparisonSpec
		fits     []types.FitSpec
		def      *types.WeightSpec
		reason   string
		requires string
	}{
		{"name missing", []types.FitComparisonSpec{{Restricted: "m0", Full: "m1"}}, pair(ols("a"), ols("a", "b")), nil, "name_missing", ""},
		{"name duplicate", append(one(""), one("")...), pair(ols("a"), ols("a", "b")), nil, "name_duplicate", ""},
		{"fit missing", []types.FitComparisonSpec{{Name: "c", Full: "m1"}}, pair(ols("a"), ols("a", "b")), nil, "fit_missing", ""},
		{"fit unknown", []types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "mx"}}, pair(ols("a"), ols("a", "b")), nil, "fit_unknown", ""},
		{"from sweep", one(""), []types.FitSpec{{Name: "m0", Request: fitReq(ols("a"))}, {Name: "m1", FromSweep: &types.FitFromSweep{Regression: "r"}}}, nil, "from_sweep", ""},
		{"fit kind", one(""), []types.FitSpec{{Name: "m0", Request: fitReq(ols("a"))},
			{Name: "m1", Request: &types.Request{Cohort: &types.Cohort{Filename: "c.pulse"}, Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Fields: []string{"a", "b"}}}}}}, nil, "fit_kind", ""},
		{"same fit", []types.FitComparisonSpec{{Name: "c", Restricted: "m1", Full: "m1"}}, pair(ols("a"), ols("a", "b")), nil, "same_fit", ""},
		{"bayes", one(""), pair(ols("a"), &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"a", "b"}}), nil, "type_unsupported", ""},
		{"type mismatch", one(""), pair(ols("a"), glm("poisson", "a", "b")), nil, "type_mismatch", ""},
		{"test unknown", one("score"), pair(ols("a"), ols("a", "b")), nil, "test_unknown", ""},
		{"penalty", one(""), pair(ols("a"), with(ols("a", "b"), func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 1 })), nil, "requires", RequiresUnpenalized},
		{"selection", one(""), pair(with(ols("a"), func(s *types.RegressionSpec) { s.Selection = "forward" }), ols("a", "b")), nil, "requires", RequiresNoSelection},
		{"bounds", one(""), pair(ols("a"), with(ols("a", "b"), func(s *types.RegressionSpec) {
			s.Bounds = &types.RegressionBounds{Nonnegative: []string{"a"}}
		})), nil, "requires", RequiresUnbounded},
		{"not nested", one(""), pair(ols("a", "c"), ols("a", "b")), nil, "not_nested", ""},
		{"swapped", one(""), pair(ols("a", "b"), ols("a")), nil, "not_nested", ""},
		{"intercept only in the restricted", one(""), pair(ols("a"), with(ols("a", "b"), func(s *types.RegressionSpec) { f := false; s.Intercept = &f })), nil, "not_nested", ""},
		{"no extra term", one(""), pair(ols("b", "a"), ols("a", "b")), nil, "no_extra_terms", ""},
		{"family differs", one(""), pair(glm("poisson", "a"), glm("negative_binomial", "a", "b")), nil, "request_mismatch", ""},
		{"filters differ", one(""), func() []types.FitSpec {
			f := pair(ols("a"), ols("a", "b"))
			f[1].Request.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "a", Values: []string{"0", "1"}}}
			return f
		}(), nil, "request_mismatch", ""},
		{"covariance differs", one(""), pair(ols("a"), with(ols("a", "b"), func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3}
		})), nil, "request_mismatch", ""},
		{"f on a fixed dispersion", one(types.ComparisonTestF), pair(glm("binomial", "a"), glm("binomial", "a", "b")), nil, "requires", RequiresEstimatedDispersion},
		{"f on theta estimated", one(types.ComparisonTestF), pair(glm("negative_binomial", "a"), glm("negative_binomial", "a", "b")), nil, "requires", RequiresEstimatedDispersion},
		{"lrt under a probability weight", one(types.ComparisonTestLRT), pair(
			with(glm("poisson", "a"), func(s *types.RegressionSpec) { s.Weight = prob }),
			with(glm("poisson", "a", "b"), func(s *types.RegressionSpec) { s.Weight = prob })), nil, "requires", RequiresNoProbabilityWeight},
		{"f under the default probability weight", one(""), pair(ols("a"), ols("a", "b")),
			&types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}, "requires", RequiresNoProbabilityWeight},
		{"wald under resample", one(types.ComparisonTestWald), pair(
			with(ols("a"), func(s *types.RegressionSpec) { s.Resample = "jackknife" }),
			with(ols("a", "b"), func(s *types.RegressionSpec) { s.Resample = "jackknife" })), nil, "requires", RequiresNoResample},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Check(tc.compare, tc.fits, tc.def)
			if err == nil {
				t.Fatal("accepted")
			}
			last := len(tc.compare) - 1
			if err.Code != errors.PULSE_COMPARISON_INVALID || err.Details["reason"] != tc.reason || err.Details["compare"] != last {
				t.Fatalf("got %s %v, want reason %s at compare %d", err.Code, err.Details, tc.reason, last)
			}
			if tc.requires != "" && err.Details["requires"] != tc.requires {
				t.Fatalf("requires %v, want %s", err.Details["requires"], tc.requires)
			}
		})
	}
}

// TestCheck_Plans: the resolved test (R's anova default), distribution
// and tested terms — the intercept a term like any other, the reporting
// knobs and a default-equal link never a rows mismatch, a frequency or
// opted-out weight never the probability refusal.
func TestCheck_Plans(t *testing.T) {
	t.Parallel()

	nbFixed := func(disp string, preds ...string) *types.RegressionSpec {
		s := glm("negative_binomial", preds...)
		s.Theta, s.Dispersion = 1.7, disp
		return s
	}
	robust := func(s *types.RegressionSpec) *types.RegressionSpec {
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3}
		return s
	}
	for _, tc := range []struct {
		name   string
		test   types.ComparisonTest
		r0, r1 *types.RegressionSpec
		want   types.ComparisonTest
		dist   types.ComparisonDistribution
		terms  []string
		loglik bool
	}{
		{"ols default", "", ols("a"), ols("a", "b", "c"), types.ComparisonTestF, types.ComparisonDistributionF, []string{"b", "c"}, false},
		{"ols lrt", types.ComparisonTestLRT, ols("a"), ols("a", "b"), types.ComparisonTestLRT, types.ComparisonDistributionChiSquare, []string{"b"}, false},
		{"ols wald", types.ComparisonTestWald, robust(ols("a")), robust(ols("a", "b")), types.ComparisonTestWald, types.ComparisonDistributionF, []string{"b"}, false},
		{"ols origin", "", func() *types.RegressionSpec { f := false; s := ols("a"); s.Intercept = &f; return s }(), ols("b", "a"),
			types.ComparisonTestF, types.ComparisonDistributionF, []string{InterceptTerm, "b"}, false},
		{"logit default", "", glm("binomial", "a"), glm("binomial", "b", "a"), types.ComparisonTestLRT, types.ComparisonDistributionChiSquare, []string{"b"}, false},
		{"logit wald", types.ComparisonTestWald, glm("binomial", "a"), glm("binomial", "a", "b"), types.ComparisonTestWald, types.ComparisonDistributionChiSquare, []string{"b"}, false},
		{"gamma default", "", glm("gamma", "a"), glm("gamma", "a", "b"), types.ComparisonTestF, types.ComparisonDistributionF, []string{"b"}, false},
		{"gamma wald", types.ComparisonTestWald, glm("gamma", "a"), glm("gamma", "a", "b"), types.ComparisonTestWald, types.ComparisonDistributionF, []string{"b"}, false},
		{"gamma robust wald", types.ComparisonTestWald, robust(glm("gamma", "a")), robust(glm("gamma", "a", "b")), types.ComparisonTestWald, types.ComparisonDistributionChiSquare, []string{"b"}, false},
		{"negbin default", "", glm("negative_binomial", "a"), glm("negative_binomial", "a", "b"), types.ComparisonTestLRT, types.ComparisonDistributionChiSquare, []string{"b"}, true},
		{"negbin fixed theta", "", nbFixed("", "a"), nbFixed("", "a", "b"), types.ComparisonTestLRT, types.ComparisonDistributionChiSquare, []string{"b"}, false},
		{"negbin fixed theta estimated dispersion", "", nbFixed(types.NegBinDispersionEstimated, "a"), nbFixed(types.NegBinDispersionEstimated, "a", "b"),
			types.ComparisonTestF, types.ComparisonDistributionF, []string{"b"}, false},
		{"reporting knobs and a canonical link", "", func() *types.RegressionSpec {
			s := glm("poisson", "a")
			s.Name, s.Vcov, s.ConfLevel, s.Save = "small", true, 0.9, "m.pulse-model.json"
			s.Margins = &types.RegressionMargins{}
			return s
		}(), func() *types.RegressionSpec { s := glm("poisson", "a", "b"); s.Link = "log"; return s }(),
			types.ComparisonTestLRT, types.ComparisonDistributionChiSquare, []string{"b"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fits := pair(tc.r0, tc.r1)
			fits[0].Request.Label, fits[0].Request.Weight = "a", &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
			fits[1].Request.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
			plans, err := Check(one(tc.test), fits, &types.WeightSpec{Field: "w"})
			if err != nil {
				t.Fatalf("refused: %v %v", err, err.Details)
			}
			p := plans[0]
			if p.Test != tc.want || p.Distribution != tc.dist || !slices.Equal(p.Terms, tc.terms) || p.LogLik != tc.loglik {
				t.Fatalf("plan %+v, want %s %s %v loglik %v", p, tc.want, tc.dist, tc.terms, tc.loglik)
			}
		})
	}
	// An opted-out slot weight is no probability weight, whatever the
	// instance default.
	r0, r1 := ols("a"), ols("a", "b")
	r0.Weight, r1.Weight = types.NullSlotWeight(), types.NullSlotWeight()
	if _, err := Check(one(""), pair(r0, r1), &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}); err != nil {
		t.Fatalf("opted-out weight refused: %v", err)
	}
}

// figures is a fit's comparison figures over terms.
func figures(terms []string, coefs []float64, cov [][]float64, dev, df, disp, waldDF float64) *model.RegressionFigures {
	return &model.RegressionFigures{Terms: terms, Coefficients: coefs, Covariance: cov, Deviance: dev, DFResidual: df, Dispersion: disp, WaldDF: waldDF}
}

// TestCompute_HandComputed: the F, likelihood-ratio and Wald statistics
// and tails on hand-computed figures; unequal rows and a failed fit fail
// the comparison alone; a V_SS that is not positive definite nulls the
// Wald figures.
func TestCompute_HandComputed(t *testing.T) {
	t.Parallel()

	terms := []string{InterceptTerm, "a", "b", "c"}
	full := &types.RegressionResult{NObs: 30, TwoLogLik: -40}
	g1 := figures(terms, []float64{1, 2, 0.5, -0.25}, [][]float64{
		{1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 0.04, 0.01}, {0, 0, 0.01, 0.0625}}, 20, 26, 20.0/26, 26)
	restricted := &types.RegressionResult{NObs: 30, TwoLogLik: -55}
	g0 := figures(terms[:2], []float64{1, 2}, nil, 32, 28, 32.0/28, 28)
	p := Plan{Name: "c", Restricted: "m0", Full: "m1", Terms: []string{"b", "c"}}

	near := func(what string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-14*math.Max(1, math.Abs(want)) {
			t.Errorf("%s = %.17g, want %.17g", what, got, want)
		}
	}
	p.Test, p.Distribution = types.ComparisonTestF, types.ComparisonDistributionF
	r, err := Compute(p, restricted, full, g0, g1)
	if err != nil || r.Error != nil {
		t.Fatalf("F: %v %+v", err, r.Error)
	}
	near("F", r.Statistic, (12.0/2)/(20.0/26))
	near("F df_residual", r.DFResidual, 26)
	if r.DF != 2 || r.NObs != 30 || r.RestrictedDeviance != 32 || r.FullDFResidual != 26 {
		t.Errorf("F figures %+v", r)
	}

	p.Test, p.Distribution, p.FixedDispersion = types.ComparisonTestLRT, types.ComparisonDistributionChiSquare, true
	r, _ = Compute(p, restricted, full, g0, g1)
	near("LRT fixed", r.Statistic, 12)
	near("LRT fixed p", r.PValue, math.Exp(-6)) // χ²(2) tail is e^(−x/2)
	if r.Dispersion != 1 || r.DFResidual != 0 {
		t.Errorf("LRT fixed figures %+v", r)
	}
	p.FixedDispersion = false
	r, _ = Compute(p, restricted, full, g0, g1)
	near("LRT estimated", r.Statistic, 12/(20.0/26))
	p.LogLik = true
	r, _ = Compute(p, restricted, full, g0, g1)
	near("LRT loglik", r.Statistic, 15)
	if r.FullTwoLogLik != -40 || r.RestrictedTwoLogLik != -55 || r.Dispersion != 0 {
		t.Errorf("LRT loglik figures %+v", r)
	}
	p.LogLik = false

	// Wald: b = (0.5, −0.25), V = [[0.04, 0.01], [0.01, 0.0625]].
	det := 0.04*0.0625 - 0.01*0.01
	w := (0.5*0.5*0.0625 - 2*0.5*-0.25*0.01 + -0.25*-0.25*0.04) / det
	p.Test, p.Distribution = types.ComparisonTestWald, types.ComparisonDistributionChiSquare
	r, _ = Compute(p, restricted, full, g0, g1)
	near("Wald chisq", r.Statistic, w)
	near("Wald chisq p", r.PValue, math.Exp(-w/2))
	p.Distribution = types.ComparisonDistributionF
	r, _ = Compute(p, restricted, full, g0, g1)
	near("Wald F", r.Statistic, w/2)
	near("Wald F df", r.DFResidual, 26)

	// A singular V_SS: undefined, never a failure.
	g := *g1
	g.Covariance = [][]float64{{1, 0, 0, 0}, {0, 1, 0, 0}, {0, 0, 1, 1}, {0, 0, 1, 1}}
	r, err = Compute(p, restricted, full, g0, &g)
	if err != nil || r.Error != nil || !math.IsNaN(r.Statistic) || !math.IsNaN(r.PValue) {
		t.Errorf("singular V_SS: %v %+v", err, r)
	}

	// Unequal rows fail alone.
	short := *full
	short.NObs = 29
	r, err = Compute(p, restricted, &short, g0, g1)
	if err != nil || r.Error == nil || r.Error.Code != string(errors.PULSE_COMPARISON_ROWS_DIFFER) || r.Error.Details["full_n_obs"] != 29 {
		t.Errorf("rows differ: %v %+v", err, r.Error)
	}
	// A failed fit fails alone with its own code, details.fit naming it.
	failed := &types.RegressionResult{Error: &types.SlotError{Code: string(errors.PROCESSING_REGRESSION_RANK_DEFICIENT), Message: "rank"}}
	r, err = Compute(p, restricted, failed, g0, nil)
	if err != nil || r.Error == nil || r.Error.Code != string(errors.PROCESSING_REGRESSION_RANK_DEFICIENT) || r.Error.Details["fit"] != "m1" {
		t.Errorf("failed fit: %v %+v", err, r.Error)
	}
	// No figures: an invariant violation.
	if _, err := Compute(p, restricted, full, g0, nil); err == nil {
		t.Error("a fit without figures computed")
	}
}

// TestRegCompare_ImportBoundary: the leaf is no-execute — predict
// imports it — so it never reaches the engine or the service.
func TestRegCompare_ImportBoundary(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, banned := range []string{"/internal/processing", "/internal/service", "/internal/descriptor"} {
				if strings.Contains(path, banned) {
					t.Errorf("%s imports %s", e.Name(), path)
				}
			}
		}
	}
}
