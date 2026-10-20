package regression

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// glmPenaltyCase is one glm_penalty.json case
// (scripts/reference/gen_glm_penalty.R): glmnet(standardize = FALSE,
// thresh = 1e-20) on rows [predictors..., y, w].
type glmPenaltyCase struct {
	Name         string      `json:"name"`
	Family       string      `json:"family"`
	Penalty      string      `json:"penalty"`
	Alpha        float64     `json:"alpha"`
	L1Ratio      float64     `json:"l1_ratio"`
	Predictors   []string    `json:"predictors"`
	Weighted     bool        `json:"weighted"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	StdErrors    []*float64  `json:"std_errors"`
	Zero         []string    `json:"zero"`
	Deviance     float64     `json:"deviance"`
}

func loadGLMPenaltyReference(t *testing.T) []glmPenaltyCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "glm_penalty.json"))
	if err != nil {
		t.Fatalf("read glm_penalty.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		Packages map[string]string `json:"packages"`
		Cases    []glmPenaltyCase  `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode glm_penalty.json: %v", err)
	}
	if doc.Packages["glmnet"] == "" || len(doc.Cases) == 0 {
		t.Fatalf("glm_penalty.json has no glmnet version or no cases")
	}
	return doc.Cases
}

func (c glmPenaltyCase) records() []Record {
	p := len(c.Predictors)
	recs := make([]Record, len(c.Rows))
	for i, row := range c.Rows {
		m := map[string]float64{"y": row[p], "w": row[p+1]}
		for j, name := range c.Predictors {
			m[name] = row[j]
		}
		recs[i] = mockRecord{values: m}
	}
	return recs
}

func (c glmPenaltyCase) spec() *types.RegressionSpec {
	spec := &types.RegressionSpec{
		Type: types.REG_GLM, Target: "y", Predictors: c.Predictors, Family: c.Family,
		Penalty: c.Penalty, Alpha: c.Alpha, Tol: 1e-13, MaxIters: 200,
	}
	if c.Penalty == "elasticnet" {
		spec.L1Ratio = c.L1Ratio
	}
	if c.Weighted {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return spec
}

// TestRegGLM_PenaltyMatchesGlmnet: every penalised binomial / poisson
// case — l2, l1, elasticnet, weighted, and a completely separated design
// whose MLE does not exist — reaches glmnet(standardize = FALSE)'s
// coefficients (a predictor glmnet zeroes is exactly 0), the l2
// penalised-Fisher sandwich standard errors (l1 / elasticnet: std error,
// p-value and interval NaN on every coefficient — no valid inference
// after data-driven selection) and the unpenalised deviance at the
// estimate, every figure within relative 1e-10 (worst seen 2e-12).
func TestRegGLM_PenaltyMatchesGlmnet(t *testing.T) {
	cases := loadGLMPenaltyReference(t)
	seen := map[string]bool{}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			seen[c.Family+"/"+c.Penalty] = true
			if len(c.Zero) > 0 {
				seen["zeroed"] = true
			}
			res, err := asBuffered(t, newGLM(t, c.spec())).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			if res.Penalty != c.Penalty || res.Alpha != c.Alpha {
				t.Errorf("echo penalty=%q alpha=%v, want %q %v", res.Penalty, res.Alpha, c.Penalty, c.Alpha)
			}
			for j, key := range coefficientKeys(c.Predictors) {
				got := res.Coefficients[key]
				if !oracleClose(got, c.Coefficients[j], 1e-10) {
					t.Errorf("β[%s] = %.17g, glmnet %.17g", key, got, c.Coefficients[j])
				}
				if c.Penalty != "l2" {
					// l1 / elasticnet: no inference (every SE null in
					// the oracle; assertInferenceNull below).
					if c.StdErrors[j] != nil {
						t.Errorf("golden %s carries SE[%s] %v; l1 / elasticnet carry none", c.Name, key, *c.StdErrors[j])
					}
					continue
				}
				se, has := res.StdErrors[key]
				if c.StdErrors[j] == nil || !has || !oracleClose(se, *c.StdErrors[j], 1e-10) {
					t.Errorf("SE[%s] = %.17g (present %v), sandwich %v", key, se, has, c.StdErrors[j])
				}
			}
			for _, name := range c.Zero {
				if res.Coefficients[name] != 0 {
					t.Errorf("β[%s] = %v, glmnet zeroed it", name, res.Coefficients[name])
				}
			}
			if c.Penalty != "l2" {
				assertInferenceNull(t, res, coefficientKeys(c.Predictors))
			}
			if !oracleClose(res.Deviance, c.Deviance, 1e-10) {
				t.Errorf("deviance = %.17g, oracle %.17g", res.Deviance, c.Deviance)
			}
		})
	}
	for _, want := range []string{"binomial/l2", "binomial/l1", "binomial/elasticnet", "poisson/l2", "poisson/l1", "poisson/elasticnet", "zeroed"} {
		if !seen[want] {
			t.Errorf("golden lacks a %s case", want)
		}
	}
}

// TestRegGLM_SeparatedFitsWithL2: the separated fixture the SEPARATION
// refusal's fixup speaks to (glm_irls.json complete_separation) fits
// under penalty: l2 — finite coefficients and standard errors — where
// the unpenalised fit refuses.
func TestRegGLM_SeparatedFitsWithL2(t *testing.T) {
	var sep glmIRLSCase
	for _, c := range loadGLMIRLSReference(t) {
		if c.Name == "complete_separation" {
			sep = c
		}
	}
	if sep.Name == "" {
		t.Fatal("glm_irls.json lost complete_separation")
	}
	spec := &types.RegressionSpec{Type: types.REG_GLM, Target: sep.Target, Predictors: []string{sep.Predictor}, Family: "binomial"}
	if _, err := asBuffered(t, newGLM(t, spec)).FitBuffered(sep.records()); !errors.HasCode(err, errors.PROCESSING_REGRESSION_SEPARATION) {
		t.Fatalf("unpenalised err = %v, want SEPARATION", err)
	}
	spec.Penalty, spec.Alpha = "l2", 0.1
	res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(sep.records())
	if err != nil {
		t.Fatalf("l2 fit: %v", err)
	}
	for _, key := range coefficientKeys(spec.Predictors) {
		b, se := res.Coefficients[key], res.StdErrors[key]
		if math.IsNaN(b) || math.IsInf(b, 0) || !(se > 0) || math.IsInf(se, 0) {
			t.Errorf("%s: β = %v, SE = %v; want finite", key, b, se)
		}
	}
	if res.Coefficients[sep.Predictor] <= 0 {
		t.Errorf("slope %v, want positive (y = 1 exactly when x ≥ 0)", res.Coefficients[sep.Predictor])
	}
}

// TestRegGLM_PenaltyAccepted is the flipped TestRegGLM_PenaltyRejected:
// a well-formed l1 / l2 / elasticnet penalty builds on binomial and
// poisson; the knob coupling (internal/regpenalty, shared with predict)
// and a penalty on gamma still refuse PROCESSING_CONFIG.
func TestRegGLM_PenaltyAccepted(t *testing.T) {
	cases := []struct {
		name   string
		mod    func(*types.RegressionSpec)
		refuse bool
	}{
		{"l1", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.1 }, false},
		{"l2", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }, false},
		{"elasticnet", func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.1, 0.5 }, false},
		{"poisson l2", func(s *types.RegressionSpec) { s.Family, s.Penalty, s.Alpha = "poisson", "l2", 0.1 }, false},
		{"l2 without alpha", func(s *types.RegressionSpec) { s.Penalty = "l2" }, true},
		{"elasticnet l1_ratio 1", func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.1, 1 }, true},
		{"alpha without penalty", func(s *types.RegressionSpec) { s.Alpha = 0.1 }, true},
		{"l1_ratio without penalty", func(s *types.RegressionSpec) { s.L1Ratio = 0.5 }, true},
		{"unknown penalty", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l3", 0.1 }, true},
		{"gamma l2", func(s *types.RegressionSpec) { s.Family, s.Penalty, s.Alpha = "gamma", "l2", 0.1 }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial"}
			c.mod(spec)
			_, err := newGLMEngine(spec, schemaWithFields("y", "x"))
			if c.refuse != (err != nil) {
				t.Fatalf("err = %v, want refused %v", err, c.refuse)
			}
			if c.refuse && !errors.HasCode(err, errors.PROCESSING_CONFIG) {
				t.Errorf("err = %v, want PROCESSING_CONFIG", err)
			}
		})
	}
}

// penaltyFixture is glm_penalty.json's binomial_l2 case.
func penaltyFixture(t *testing.T, name string) glmPenaltyCase {
	t.Helper()
	for _, c := range loadGLMPenaltyReference(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("glm_penalty.json lost %s", name)
	return glmPenaltyCase{}
}

// TestRegGLM_PenaltyVcov: an l2 fit reports vcov — the penalised
// sandwich, √diag = std_errors; l1 / elasticnet refuse vcov up front
// PROCESSING_REGRESSION_VCOV_UNSUPPORTED (reason penalty), alone or under
// a modifier.
func TestRegGLM_PenaltyVcov(t *testing.T) {
	c := penaltyFixture(t, "binomial_l2")
	spec := c.spec()
	spec.Vcov = true
	res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(c.records())
	if err != nil {
		t.Fatalf("l2 vcov fit: %v", err)
	}
	if res.Vcov == nil {
		t.Fatal("l2 fit with vcov: Vcov absent")
	}
	for i, key := range res.Vcov.RowKeys {
		if d := math.Sqrt(res.Vcov.Values[i][i]); d != res.StdErrors[key] {
			t.Errorf("√Vcov[%s] = %.17g, SE %.17g", key, d, res.StdErrors[key])
		}
	}
	for _, mod := range []func(*types.RegressionSpec){
		func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.1 },
		func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.1, 0.5 },
		func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Resample = "l1", 0.1, "jackknife" },
	} {
		s := &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial", Vcov: true}
		mod(s)
		_, err := newGLMEngine(s, schemaWithFields("y", "x"))
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED || ce.Details["reason"] != "penalty" {
			t.Errorf("penalty %s resample %q vcov: err = %v, want VCOV_UNSUPPORTED reason penalty", s.Penalty, s.Resample, err)
		}
	}
}

// TestRegGLM_PenaltyModifiers: a penalty rides the resample and
// selection modifiers — every jackknife replicate and every selection
// refit is penalised, so the point estimate is the penalised one.
func TestRegGLM_PenaltyModifiers(t *testing.T) {
	c := penaltyFixture(t, "binomial_l2")
	base, err := asBuffered(t, newGLM(t, c.spec())).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	jk := c.spec()
	jk.Resample = "jackknife"
	res, err := asBuffered(t, newGLM(t, jk)).FitBuffered(c.records())
	if err != nil {
		t.Fatalf("l2 + jackknife: %v", err)
	}
	for _, key := range coefficientKeys(c.Predictors) {
		if res.Coefficients[key] != base.Coefficients[key] {
			t.Errorf("jackknife β[%s] = %v, penalised base %v", key, res.Coefficients[key], base.Coefficients[key])
		}
		if !(res.StdErrors[key] > 0) || res.StdErrors[key] == base.StdErrors[key] {
			t.Errorf("jackknife SE[%s] = %v (sandwich %v); want a replicate SE", key, res.StdErrors[key], base.StdErrors[key])
		}
	}
	// Unpenalised jackknife lands elsewhere: the replicates are penalised.
	plain := c.spec()
	plain.Penalty, plain.Alpha, plain.Resample = "", 0, "jackknife"
	pres, err := asBuffered(t, newGLM(t, plain)).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	if pres.StdErrors["x1"] == res.StdErrors["x1"] {
		t.Error("penalised and unpenalised jackknife SEs coincide; the replicates are not penalised")
	}

	sel := c.spec()
	sel.Selection, sel.Criterion = "backward", "aic"
	sres, err := asBuffered(t, newGLM(t, sel)).FitBuffered(c.records())
	if err != nil {
		t.Fatalf("l2 + selection: %v", err)
	}
	if sres.Penalty != "l2" || len(sres.SelectedFeatures) == 0 {
		t.Errorf("selection result penalty=%q selected=%v", sres.Penalty, sres.SelectedFeatures)
	}
	refit := c.spec()
	refit.Predictors = sres.SelectedFeatures
	want, err := asBuffered(t, newGLM(t, refit)).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range coefficientKeys(sres.SelectedFeatures) {
		if sres.Coefficients[key] != want.Coefficients[key] {
			t.Errorf("selection β[%s] = %v, penalised refit %v", key, sres.Coefficients[key], want.Coefficients[key])
		}
	}
}

// TestGLMPenalty_ActiveSetMatchesDescent: the coordinate-descent solve
// with its exact active-set finish equals plain descent run to its own
// tolerance (the finish is a shortcut, not a different answer).
func TestGLMPenalty_ActiveSetMatchesDescent(t *testing.T) {
	// A 3 × 3 working system with one predictor inside the l1 bound.
	a := []float64{10, 2, 1, 2, 8, 0.5, 1, 0.5, 6}
	b := []float64{3, 4, 0.2}
	p := &glmPenalty{kind: "l1", alpha: 0.05, l1Ratio: 1, nStar: 10}
	got, ok := p.solve(3, a, b, nil)
	if !ok {
		t.Fatal("solve failed")
	}
	if got[2] != 0 {
		t.Errorf("β₂ = %v, want 0 (|r₂| inside λ₁)", got[2])
	}
	// Plain descent from the same start, no finish.
	beta := make([]float64, 3)
	l1 := p.lambda1()
	for sweep := 0; sweep < 10000; sweep++ {
		for j := 0; j < 3; j++ {
			r := b[j]
			for c := 0; c < 3; c++ {
				if c != j {
					r -= a[j*3+c] * beta[c]
				}
			}
			if j == 0 {
				beta[0] = r / a[0]
			} else {
				beta[j] = softThreshold(r, l1) / a[j*3+j]
			}
		}
	}
	for j := range beta {
		if math.Abs(got[j]-beta[j]) > 1e-13 {
			t.Errorf("β[%d] = %.17g, plain descent %.17g", j, got[j], beta[j])
		}
	}
}

// TestRegGLM_PenalizedStopIsNeverSeparation: a penalised estimate is
// finite, so a penalised fit stopped short on separated data (a tiny
// alpha, a low iteration cap) is NO_CONVERGE — never SEPARATION, whose
// message says the estimate does not exist.
func TestRegGLM_PenalizedStopIsNeverSeparation(t *testing.T) {
	var sep glmIRLSCase
	for _, c := range loadGLMIRLSReference(t) {
		if c.Name == "complete_separation" {
			sep = c
		}
	}
	spec := &types.RegressionSpec{
		Type: types.REG_GLM, Target: sep.Target, Predictors: []string{sep.Predictor}, Family: "binomial",
		Penalty: "l2", Alpha: 1e-12, MaxIters: 12,
	}
	_, err := asBuffered(t, newGLM(t, spec)).FitBuffered(sep.records())
	if !errors.HasCode(err, errors.PROCESSING_REGRESSION_NO_CONVERGE) {
		t.Fatalf("err = %v, want PROCESSING_REGRESSION_NO_CONVERGE", err)
	}
}
