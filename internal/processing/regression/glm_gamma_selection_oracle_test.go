package regression

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// glmGammaSelectionDoc is glm_gamma_selection.json
// (scripts/reference/gen_glm_gamma_selection.R).
type glmGammaSelectionDoc struct {
	RVersion string  `json:"r_version"`
	NRows    int     `json:"n_rows"`
	NEff     float64 `json:"n_eff"`
	Rows     []struct {
		X1 float64 `json:"x1"`
		X2 float64 `json:"x2"`
		X3 float64 `json:"x3"`
		Y  float64 `json:"y"`
		F  float64 `json:"f"`
		P  float64 `json:"p"`
	} `json:"rows"`
	Models []struct {
		Predictors        []string `json:"predictors"`
		Rank              int      `json:"rank"`
		Deviance          float64  `json:"deviance"`
		LogLik            float64  `json:"loglik"`
		AIC               float64  `json:"aic"`
		BIC               float64  `json:"bic"`
		Phi1AIC           float64  `json:"phi1_aic"`
		Phi1BIC           float64  `json:"phi1_bic"`
		FrequencyLogLik   float64  `json:"frequency_loglik"`
		FrequencyAIC      float64  `json:"frequency_aic"`
		FrequencyBIC      float64  `json:"frequency_bic"`
		ProbabilityLogLik float64  `json:"probability_loglik"`
		ProbabilityAIC    float64  `json:"probability_aic"`
		ProbabilityBIC    float64  `json:"probability_bic"`
	} `json:"models"`
	Runs []struct {
		Selection  string   `json:"selection"`
		Criterion  string   `json:"criterion"`
		RStepTerms []string `json:"r_step_terms"`
		RStepPath  []string `json:"r_step_path"`
		Phi1Terms  []string `json:"phi1_terms"`
	} `json:"runs"`
}

func loadGLMGammaSelectionReference(t *testing.T) glmGammaSelectionDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "glm_gamma_selection.json"))
	if err != nil {
		t.Fatalf("read glm_gamma_selection.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc glmGammaSelectionDoc
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode glm_gamma_selection.json: %v", err)
	}
	if doc.RVersion == "" || len(doc.Models) == 0 || len(doc.Runs) == 0 || len(doc.Rows) != doc.NRows {
		t.Fatalf("glm_gamma_selection.json is incomplete")
	}
	return doc
}

func (d glmGammaSelectionDoc) records() []Record {
	recs := make([]Record, len(d.Rows))
	for i, r := range d.Rows {
		recs[i] = mockRecord{values: map[string]float64{"x1": r.X1, "x2": r.X2, "x3": r.X3, "y": r.Y, "f": r.F, "p": r.P}}
	}
	return recs
}

// gammaSelectionEngine builds the modifier engine a gamma REG_GLM
// selection spec routes to; weightField/kind stamp a slot weight the way
// the resolver would (it refuses one on a selection slot, so the weighted
// rows pin the criterion helper's prior-weight rule, not a runtime arm).
func gammaSelectionEngine(t *testing.T, selection, criterion, weightField string, kind types.WeightKind) *modifierEngine {
	t.Helper()
	spec := &types.RegressionSpec{
		Type: types.REG_GLM, Family: "gamma", Link: "inverse",
		Target: "y", Predictors: []string{"x1", "x2", "x3"},
		Selection: selection, Criterion: criterion,
		Tol: 1e-14, MaxIters: 100,
	}
	if weightField != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: weightField, Kind: kind})
	}
	eng, err := newGLMEngine(spec, schemaWithFields("y", "x1", "x2", "x3", "f", "p"))
	if err != nil {
		t.Fatalf("newGLMEngine: %v", err)
	}
	me, ok := eng.(*modifierEngine)
	if !ok {
		t.Fatalf("selection spec built %T, want *modifierEngine", eng)
	}
	return me
}

// TestReferenceOracle_GLMGammaAICMatchesR: the score a gamma REG_GLM
// selection ranks candidates by is R's AIC(fit) / BIC(fit) — the gamma
// log-likelihood at dispersion deviance / N* (Gamma()$aic) with the
// dispersion counted as a parameter — within relative 1e-10 for every
// subset of {x1, x2, x3}: unweighted, frequency-weighted (= the rep()
// expansion: BIC on log Σf, not R's glm(weights =) row count) and
// probability-weighted (the frequency formula on w*: AIC(glm(weights =
// w*)), BIC on log n_eff). The fixed-dispersion score it replaced
// (deviance + 2·rank) differs on every model.
func TestReferenceOracle_GLMGammaAICMatchesR(t *testing.T) {
	t.Parallel()

	doc := loadGLMGammaSelectionReference(t)
	recs := doc.records()
	near := func(t *testing.T, what string, got, want float64) {
		t.Helper()
		if !oracleClose(got, want, 1e-10) {
			t.Errorf("%s = %.17g, R %.17g", what, got, want)
		}
	}
	type arm struct {
		name, field string
		kind        types.WeightKind
		pick        func(i int) (logL, aic, bic float64)
	}
	arms := []arm{
		{"unweighted", "", "", func(i int) (float64, float64, float64) {
			m := doc.Models[i]
			return m.LogLik, m.AIC, m.BIC
		}},
		{"frequency", "f", types.WeightKindFrequency, func(i int) (float64, float64, float64) {
			m := doc.Models[i]
			return m.FrequencyLogLik, m.FrequencyAIC, m.FrequencyBIC
		}},
		{"probability", "p", types.WeightKindProbability, func(i int) (float64, float64, float64) {
			m := doc.Models[i]
			return m.ProbabilityLogLik, m.ProbabilityAIC, m.ProbabilityBIC
		}},
	}
	for _, a := range arms {
		t.Run(a.name, func(t *testing.T) {
			aicEng := gammaSelectionEngine(t, "forward", "aic", a.field, a.kind)
			bicEng := gammaSelectionEngine(t, "forward", "bic", a.field, a.kind)
			refit, aicFn, bicFn := aicEng.makeRefitFitter(), aicEng.makeCriterionFn(), bicEng.makeCriterionFn()
			for i, m := range doc.Models {
				label := "[" + strings.Join(m.Predictors, "+") + "]"
				fit, err := refit(recs, m.Predictors)
				if err != nil {
					t.Fatalf("%s refit: %v", label, err)
				}
				wantLogL, wantAIC, wantBIC := a.pick(i)
				gotAIC := aicFn(fit, len(m.Predictors))
				near(t, label+" AIC", gotAIC, wantAIC)
				near(t, label+" BIC", bicFn(fit, len(m.Predictors)), wantBIC)
				_, _, logL := aicBICFromGamma(fit.Y, fit.Mu, fit.Prior, fit.Deviance, len(m.Predictors))
				near(t, label+" logLik", logL, wantLogL)
				if a.field == "" {
					near(t, label+" deviance", fit.Deviance, m.Deviance)
					if oracleClose(gotAIC, m.Phi1AIC, 1e-6) {
						t.Errorf("%s AIC %v still equals the fixed-dispersion score", label, gotAIC)
					}
				}
			}
		})
	}
}

// TestReferenceOracle_GLMGammaSelectionMatchesStep: forward, backward and
// stepwise gamma selection under aic and bic end where R's step() ends on
// glm_gamma_selection.json's fixture (forward / stepwise in step()'s
// addition order), while the fixed-dispersion score the engine used
// before walks to a different model (the golden asserts it). Shape 40 —
// dispersion ≈ 0.025 — makes x2's deviance drop worth ~3.5 AIC units, so
// the φ = 1 score, which sees 0.07, never adds it.
func TestReferenceOracle_GLMGammaSelectionMatchesStep(t *testing.T) {
	t.Parallel()

	doc := loadGLMGammaSelectionReference(t)
	recs := doc.records()
	for _, r := range doc.Runs {
		t.Run(r.Selection+"/"+r.Criterion, func(t *testing.T) {
			if setEq(r.Phi1Terms, r.RStepTerms) && r.Criterion == "aic" {
				t.Fatalf("fixture does not discriminate: φ = 1 terms %v = step() terms %v", r.Phi1Terms, r.RStepTerms)
			}
			res, err := gammaSelectionEngine(t, r.Selection, r.Criterion, "", "").FitBuffered(recs)
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			got := res.SelectedFeatures
			if r.Selection == "backward" {
				if !setEq(got, r.RStepTerms) {
					t.Errorf("selected %v, R step() %v (path %v)", got, r.RStepTerms, r.RStepPath)
				}
			} else if !slices.Equal(got, r.RStepTerms) {
				t.Errorf("selected %v (in order), R step() %v (path %v)", got, r.RStepTerms, r.RStepPath)
			}
			if setEq(got, r.Phi1Terms) && !setEq(r.Phi1Terms, r.RStepTerms) {
				t.Errorf("selected the fixed-dispersion model %v", got)
			}
		})
	}
}

// TestAICBICFromGamma_PriorWeightIsExpansion: integer prior weights give
// exactly the rep() expansion's criterion, and a zero deviance (every
// row fitted exactly) is the dominant bounded score the OLS helper uses.
func TestAICBICFromGamma_PriorWeightIsExpansion(t *testing.T) {
	t.Parallel()

	y := []float64{1.2, 0.8, 2.5, 1.9}
	mu := []float64{1.1, 0.9, 2.2, 2.0}
	w := []float64{1, 3, 2, 1}
	var ye, me []float64
	dev := 0.0
	for i := range y {
		for c := 0; c < int(w[i]); c++ {
			ye = append(ye, y[i])
			me = append(me, mu[i])
		}
		dev += w[i] * gammaInverse.Deviance(y[i], mu[i])
	}
	dev *= 2
	a1, b1, l1 := aicBICFromGamma(y, mu, w, dev, 1)
	a2, b2, l2 := aicBICFromGamma(ye, me, nil, dev, 1)
	for _, c := range [][2]float64{{a1, a2}, {b1, b2}, {l1, l2}} {
		if !oracleClose(c[0], c[1], 1e-13) {
			t.Errorf("weighted %v, expansion %v", c[0], c[1])
		}
	}
	if aic, bic, _ := aicBICFromGamma(y, y, nil, 0, 1); !(aic < -1e300) || !(bic < -1e300) {
		t.Errorf("zero deviance: aic %v bic %v, want the dominant bounded score", aic, bic)
	}
}
