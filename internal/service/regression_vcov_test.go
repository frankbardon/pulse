package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// regression_vcov_test.go pins the opt-in coefficient covariance
// (RegressionSpec.Vcov → RegressionResult.Vcov / .Correlation) end to
// end against the R oracle mv_vcov.json: vcov(lm) for REG_OLS and
// vcov(glm) for REG_GLM binomial / poisson on mtcars, attitude and the
// weighted fixture — unweighted, frequency (the rep() expansion) and
// probability (the frequency formula on w* = w·n_eff/Σw).

// vcovOracle is one mv_vcov.json case.
type vcovOracle struct {
	Fixture     string      `json:"fixture"`
	Formula     string      `json:"formula"`
	Family      string      `json:"family"`
	Weight      string      `json:"weight"`
	NRows       int         `json:"n_rows"`
	NStar       float64     `json:"n_star"`
	Terms       []string    `json:"terms"`
	StdErrors   []float64   `json:"std_errors"`
	Vcov        [][]float64 `json:"vcov"`
	Correlation [][]float64 `json:"correlation"`
}

// Tolerances against R, relative (absolute below 1).
//
// vcovOLSTol: lm solves a QR, Pulse folds Welford moments and inverts
// the centred Gram by gonum's Cholesky (cross-arch ulps).
//
// vcovGLMTol is looser for a measured reason, not to absorb noise: R's
// vcov(glm) is (XᵀWX)⁻¹ at the working weights glm.fit computed at the
// START of its last IRLS iteration — one β update behind the returned
// coefficients — whereas Pulse evaluates (XᵀWX)⁻¹ at the weights of the
// CONVERGED β. Tightening Pulse's tolerance does not move its figure
// (the fit is converged); refining R's fit by Newton steps to a fixed
// point and inverting XᵀWX there reproduces Pulse to 2e-13 (pinned by
// TestRegressionVcov_GLMAtConvergedWeights). The lag is largest on the
// near-separated binomial fits (relative ≈ 1e-7 on mtcars am ~ wt + hp)
// and invisible on poisson; 1e-6 holds it with room while staying far
// below any basis mistake (w vs w* moves an entry by tens of percent).
const (
	vcovOLSTol = 1e-10
	vcovGLMTol = 1e-6
)

func vcovSpec(c vcovOracle) *types.RegressionSpec {
	lhs, rhs, _ := strings.Cut(c.Formula, "~")
	var preds []string
	for _, p := range strings.Split(rhs, "+") {
		preds = append(preds, strings.TrimSpace(p))
	}
	s := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: strings.TrimSpace(lhs), Predictors: preds, Vcov: true}
	if c.Family != "gaussian" {
		s.Type, s.Family = types.REG_GLM, c.Family
	}
	switch c.Weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_prob", Kind: types.WeightKindProbability})
	}
	return s
}

func vcovNear(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol*math.Max(1, math.Abs(want))
}

// TestRegressionVcov_MatchesROracle: every mv_vcov.json case — Vcov and
// Correlation match R's vcov / cov2cor entry for entry, keyed
// "(intercept)" then the predictors, square symmetric in full encoding;
// √diag(Vcov) is the standard error EXACTLY (the variances the SEs are
// computed from), and the inference basis is N* (the oracle's n_star)
// on a weighted fit.
func TestRegressionVcov_MatchesROracle(t *testing.T) {
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	if len(golden.Cases) < 21 {
		t.Fatalf("oracle has %d cases, want ≥ 21", len(golden.Cases))
	}
	cfg := fixtureFS(t, dir)
	for _, c := range golden.Cases {
		t.Run(fmt.Sprintf("%s/%s/%s", c.Fixture, c.Family, c.Weight), func(t *testing.T) {
			spec := vcovSpec(c)
			resp, err := New(cfg).Process(context.Background(), &types.Request{
				Cohort:      &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
				Regressions: []*types.RegressionSpec{spec},
			})
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			res := resp.Regressions[0]
			if res.Vcov == nil || res.Correlation == nil {
				t.Fatalf("vcov / correlation absent: %+v", res)
			}
			tol := vcovOLSTol
			if c.Family != "gaussian" {
				tol = vcovGLMTol
			}
			worst := 0.0
			keys := append([]string{"(intercept)"}, spec.Predictors...)
			for name, m := range map[string]*types.MatrixValues{"vcov": res.Vcov, "correlation": res.Correlation} {
				if m.Kind != types.MatrixKindSquareSymmetric || m.Encoding != types.MatrixEncodingFull ||
					!reflect.DeepEqual(m.RowKeys, keys) || !reflect.DeepEqual(m.ColumnKeys, keys) {
					t.Fatalf("%s shape %+v, want square_symmetric/full keyed %v", name, m, keys)
				}
			}
			if c.Weight == "probability" && !vcovNear(res.NEff, c.NStar, vcovOLSTol) {
				t.Errorf("n_eff %v, oracle n_star %v", res.NEff, c.NStar)
			}
			for i := range keys {
				for j := range keys {
					if g, w := res.Vcov.Values[i][j], c.Vcov[i][j]; !vcovNear(g, w, tol) {
						t.Errorf("vcov[%s][%s] = %.17g, R = %.17g", keys[i], keys[j], g, w)
					}
					worst = math.Max(worst, math.Abs(res.Vcov.Values[i][j]-c.Vcov[i][j])/math.Max(1, math.Abs(c.Vcov[i][j])))
					if g, w := res.Correlation.Values[i][j], c.Correlation[i][j]; !vcovNear(g, w, tol) {
						t.Errorf("correlation[%s][%s] = %.17g, R = %.17g", keys[i], keys[j], g, w)
					}
					if res.Vcov.Values[i][j] != res.Vcov.Values[j][i] || res.Correlation.Values[i][j] != res.Correlation.Values[j][i] {
						t.Errorf("[%s][%s] not exactly symmetric", keys[i], keys[j])
					}
				}
				if res.Correlation.Values[i][i] != 1 {
					t.Errorf("correlation diagonal [%s] = %v, want exactly 1", keys[i], res.Correlation.Values[i][i])
				}
				if se := res.StdErrors[keys[i]]; math.Sqrt(res.Vcov.Values[i][i]) != se {
					t.Errorf("√vcov[%s][%s] = %.17g, std_error %.17g (must be exact)", keys[i], keys[i], math.Sqrt(res.Vcov.Values[i][i]), se)
				}
				if !vcovNear(res.StdErrors[keys[i]], c.StdErrors[i], tol) {
					t.Errorf("std_error[%s] = %.17g, R = %.17g", keys[i], res.StdErrors[keys[i]], c.StdErrors[i])
				}
			}
			t.Logf("worst vcov relative error %.3g (tol %g)", worst, tol)
		})
	}
}

// TestRegressionVcov_OffByDefault: without vcov the result carries
// neither matrix, so the wire is byte-identical to a pre-vcov build —
// the marshalled result has no "vcov" / "correlation" key — and the
// fitted figures do not move when it is switched on.
func TestRegressionVcov_OffByDefault(t *testing.T) {
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	for _, c := range golden.Cases[:3] {
		on := vcovSpec(c)
		off := *on
		off.Vcov = false
		resp, err := New(cfg).Process(context.Background(), &types.Request{
			Cohort:      &types.Cohort{Filename: filepath.Join(dir, c.Fixture+".pulse")},
			Regressions: []*types.RegressionSpec{&off, on},
		})
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		plain, with := resp.Regressions[0], resp.Regressions[1]
		if plain.Vcov != nil || plain.Correlation != nil {
			t.Fatalf("%s: vcov emitted without opt-in", c.Formula)
		}
		raw, err := json.Marshal(plain)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"vcov"`) || strings.Contains(string(raw), `"correlation"`) {
			t.Fatalf("%s: wire carries a vcov key: %s", c.Formula, raw)
		}
		stripped := *with
		stripped.Vcov, stripped.Correlation = nil, nil
		if !reflect.DeepEqual(plain, &stripped) {
			t.Errorf("%s: vcov moved the fit:\n off %+v\n on  %+v", c.Formula, plain, &stripped)
		}
	}
}

// TestRegressionVcov_RidgeAndBayes pins the two sources the R oracle
// does not cover against their closed forms on mtcars: ridge's √diag is
// its (sandwich) standard errors exactly and its intercept row is
// −μ_xᵀ·Cov(β); Bayes is the POSTERIOR covariance b_n/(a_n−1)·Λ_n⁻¹ —
// its diagonal is the marginal-t scale² times a_n/(a_n−1), not SE².
func TestRegressionVcov_RidgeAndBayes(t *testing.T) {
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	preds := []string{"wt", "hp", "qsec"}
	ridge := &types.RegressionSpec{Name: "ridge", Type: types.REG_OLS, Target: "mpg", Predictors: preds, Penalty: "l2", Alpha: 0.05, Vcov: true}
	bayes := &types.RegressionSpec{Name: "bayes", Type: types.REG_BAYES_LINEAR, Target: "mpg", Predictors: preds, Prior: "nig", PriorPrecision: 0.01, PriorShape: 2, PriorRate: 3, Vcov: true}
	resp, err := New(cfg).Process(context.Background(), &types.Request{
		Cohort:      &types.Cohort{Filename: filepath.Join(dir, "mtcars.pulse")},
		Regressions: []*types.RegressionSpec{ridge, bayes},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	keys := append([]string{"(intercept)"}, preds...)

	r := resp.Regressions[0]
	if r.Vcov == nil {
		t.Fatal("ridge: no vcov")
	}
	for i, k := range keys {
		if math.Sqrt(r.Vcov.Values[i][i]) != r.StdErrors[k] {
			t.Errorf("ridge √vcov[%s] = %v, std_error %v", k, math.Sqrt(r.Vcov.Values[i][i]), r.StdErrors[k])
		}
	}
	// Intercept row: Cov(β₀, β_j) = −Σ_k μ_k·Cov(β_k, β_j). Column means
	// of mtcars (wt, hp, qsec).
	means := []float64{3.21725, 146.6875, 17.84875}
	for j := 1; j < len(keys); j++ {
		want := 0.0
		for k := 1; k < len(keys); k++ {
			want -= means[k-1] * r.Vcov.Values[k][j]
		}
		if !vcovNear(r.Vcov.Values[0][j], want, vcovOLSTol) {
			t.Errorf("ridge cov(intercept, %s) = %v, −μᵀ·Cov = %v", keys[j], r.Vcov.Values[0][j], want)
		}
	}

	b := resp.Regressions[1]
	if b.Vcov == nil || b.Correlation == nil {
		t.Fatal("bayes: no vcov")
	}
	// a_n = a₀ + n/2 = 2 + 16 = 18: Var = SE²·a_n/(a_n−1).
	aN := 2 + 32/2.0
	for i, k := range keys {
		want := b.StdErrors[k] * b.StdErrors[k] * aN / (aN - 1)
		if !vcovNear(b.Vcov.Values[i][i], want, vcovOLSTol) {
			t.Errorf("bayes var[%s] = %v, SE²·a_n/(a_n−1) = %v", k, b.Vcov.Values[i][i], want)
		}
	}
}

// TestRegressionVcov_Refusals: vcov on lasso / elastic net and under
// Resample / Selection is refused with PROCESSING_REGRESSION_VCOV_
// UNSUPPORTED naming the blocking setting — never approximated, never
// silently omitted — and predict reports the same code (one rule).
func TestRegressionVcov_Refusals(t *testing.T) {
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	base := types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp"}, Vcov: true}
	cases := []struct {
		name, reason string
		mut          func(*types.RegressionSpec)
	}{
		{"lasso", "penalty", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.1 }},
		{"elasticnet", "penalty", func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.1, 0.5 }},
		{"resample", "resample", func(s *types.RegressionSpec) { s.Resample = "jackknife" }},
		{"selection", "selection", func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }},
		{"glm-selection", "selection", func(s *types.RegressionSpec) {
			s.Type, s.Family, s.Target, s.Selection, s.Criterion = types.REG_GLM, "poisson", "carb", "backward", "aic"
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := base
			c.mut(&spec)
			req := &types.Request{
				Cohort:      &types.Cohort{Filename: filepath.Join(dir, "mtcars.pulse")},
				Regressions: []*types.RegressionSpec{&spec},
			}
			_, err := New(cfg).Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED {
				t.Fatalf("Process err = %v, want PROCESSING_REGRESSION_VCOV_UNSUPPORTED", err)
			}
			if ce.Details["reason"] != c.reason {
				t.Errorf("details.reason = %v, want %s", ce.Details["reason"], c.reason)
			}
			data, err := os.ReadFile(filepath.Join(dir, "mtcars.pulse"))
			if err != nil {
				t.Fatal(err)
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED) && e.Details["reason"] == c.reason {
					found = true
				}
			}
			if !found {
				t.Errorf("predict errors %+v lack VCOV_UNSUPPORTED reason %s", pred.Errors, c.reason)
			}

			// Without vcov the same fit runs.
			spec.Vcov = false
			if _, err := New(cfg).Process(context.Background(), req); err != nil {
				t.Errorf("without vcov: %v", err)
			}
		})
	}
}

// TestRegressionVcov_GLMAtConvergedWeights pins why the GLM oracle
// tolerance is 1e-6: R's glm(am ~ wt + hp, binomial) on mtcars, refined
// by Newton steps until β is a fixed point (max relative step 7.7e-16)
// and (XᵀWX)⁻¹ taken at THAT β's weights, gives the matrix below —
// Pulse matches it to 1e-10, while vcov(glm) itself sits ≈ 1e-7 away
// (its weights lag one IRLS update behind the returned coefficients).
// R: f <- glm(..., control = glm.control(epsilon = 1e-14)); b <- coef(f);
// repeat b <- b + solve(t(X) %*% (w*X), t(X) %*% (y - mu)) at
// mu = plogis(X %*% b), w = mu(1 − mu); V <- solve(t(X) %*% (w*X)).
func TestRegressionVcov_GLMAtConvergedWeights(t *testing.T) {
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	resp, err := New(cfg).Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: filepath.Join(dir, "mtcars.pulse")},
		Regressions: []*types.RegressionSpec{{
			Name: "fit", Type: types.REG_GLM, Family: "binomial", Target: "am", Predictors: []string{"wt", "hp"}, Vcov: true,
		}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want := [][]float64{
		{55.406562070782741, -22.037059881266103, 0.077817896896685251},
		{-22.037059881266103, 9.4167679457943958, -0.041832392344663613},
		{0.077817896896685251, -0.041832392344663613, 0.00031450022986933185},
	}
	got := resp.Regressions[0].Vcov.Values
	for i := range want {
		for j := range want[i] {
			if !vcovNear(got[i][j], want[i][j], 1e-10) {
				t.Errorf("vcov[%d][%d] = %.17g, R at converged weights %.17g", i, j, got[i][j], want[i][j])
			}
		}
	}
}
