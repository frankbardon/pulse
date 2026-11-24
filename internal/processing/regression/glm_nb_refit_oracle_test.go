package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/types"
)

// glm_nb_refit_oracle_test.go pins the REG_GLM negative binomial under
// selection and resample (U69 E3-S2, #638) — θ re-estimated inside every
// candidate and replicate — to glm_nb_refit.json
// (scripts/reference/gen_glm_nb_refit.R, R 4.6.1 + MASS 7.3-66):
// MASS::stepAIC on glm.nb (an exact greedy for a fixed θ) and base-R
// glm.nb replicate loops.

type glmNBRefitRun struct {
	Selection    string    `json:"selection"`
	Criterion    string    `json:"criterion"`
	ThetaFixed   float64   `json:"theta_fixed"`
	Oracle       string    `json:"oracle"`
	Terms        []string  `json:"terms"`
	Coefficients []float64 `json:"coefficients"`
	Theta        float64   `json:"theta"`
	ThetaStdErr  float64   `json:"theta_std_err"`
}

type glmNBRefitCase struct {
	Name            string    `json:"name"`
	Resample        string    `json:"resample"`
	ThetaFixed      float64   `json:"theta_fixed"`
	By              bool      `json:"jackknife_by"`
	Weighted        bool      `json:"weighted"`
	Margins         string    `json:"margins"`
	Samples         [][]int   `json:"samples"`
	Coefficients    []float64 `json:"coefficients"`
	CoefStdErrors   []float64 `json:"coef_std_errors"`
	Theta           float64   `json:"theta"`
	ThetaStdErr     float64   `json:"theta_std_err"`
	MarginEstimates []float64 `json:"margin_estimates"`
	MarginStdErrors []float64 `json:"margin_std_errors"`
}

type glmNBRefitDoc struct {
	Packages map[string]string `json:"packages"`
	Rows     [][]float64       `json:"rows"`
	Runs     []glmNBRefitRun   `json:"runs"`
	Cases    []glmNBRefitCase  `json:"cases"`
}

// loadGLMNBRefit reads glm_nb_refit.json, checking its golden-hash footer.
func loadGLMNBRefit(t *testing.T) glmNBRefitDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "glm_nb_refit.json"))
	if err != nil {
		t.Fatalf("read glm_nb_refit.json: %v (regenerate with scripts/reference/gen_glm_nb_refit.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("glm_nb_refit.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("glm_nb_refit.json: hash mismatch (hand-edited?); regenerate it")
	}
	var doc glmNBRefitDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode glm_nb_refit.json: %v", err)
	}
	if !strings.HasPrefix(doc.Packages["R"], "R version 4.6.1") || doc.Packages["MASS"] != "7.3.66" || len(doc.Runs) != 7 || len(doc.Cases) != 8 {
		t.Fatalf("glm_nb_refit.json: R %q MASS %q, %d runs, %d cases", doc.Packages["R"], doc.Packages["MASS"], len(doc.Runs), len(doc.Cases))
	}
	return doc
}

func (d glmNBRefitDoc) records() []Record {
	out := make([]Record, len(d.Rows))
	for i, r := range d.Rows {
		out[i] = mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1], "x3": r[2], "y": r[3], "g": r[4], "f": r[5]}}
	}
	return out
}

func glmNBRefitSpec(predictors []string, theta float64) *types.RegressionSpec {
	return &types.RegressionSpec{Name: "nb", Type: types.REG_GLM, Family: reglink.NegativeBinomial, Target: "y",
		Predictors: predictors, Theta: theta, Tol: 1e-13, MaxIters: 500}
}

// TestReferenceOracle_GLMNegBinSelectionMatchesStepAIC: forward,
// backward and stepwise selection on AIC and BIC re-estimate θ per
// candidate and walk MASS::stepAIC's glm.nb path (a fixed θ: the exact
// greedy over glm(negative.binomial(θ))), landing on its final model —
// coefficients, θ̂ and SE(θ̂) at the joint MLE.
func TestReferenceOracle_GLMNegBinSelectionMatchesStepAIC(t *testing.T) {
	t.Parallel()

	doc := loadGLMNBRefit(t)
	recs := doc.records()
	for _, r := range doc.Runs {
		t.Run(r.Selection+"_"+r.Criterion+"_"+r.Oracle, func(t *testing.T) {
			spec := glmNBRefitSpec([]string{"x1", "x2", "x3"}, r.ThetaFixed)
			spec.Selection, spec.Criterion = r.Selection, r.Criterion
			res, err := fitModifier(t, spec, recs)
			if err != nil {
				t.Fatalf("fit: %v", err)
			}
			got := res.SelectedFeatures
			if r.Selection == "backward" {
				got, want := slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(r.Terms))
				if !slices.Equal(got, want) {
					t.Fatalf("selected %v, R %v", got, want)
				}
			} else if !slices.Equal(got, r.Terms) {
				t.Fatalf("selected %v, R %v (addition order)", got, r.Terms)
			}
			keys := append([]string{InterceptKey}, r.Terms...)
			for j, k := range keys {
				if !relNear(res.Coefficients[k], r.Coefficients[j], 1e-7) {
					t.Errorf("coefficient %s = %.12g, R %.12g", k, res.Coefficients[k], r.Coefficients[j])
				}
			}
			if !relNear(res.Theta, r.Theta, 1e-7) {
				t.Errorf("theta = %.12g, R %.12g", res.Theta, r.Theta)
			}
			if !relNear(res.ThetaStdErr, r.ThetaStdErr, 1e-6) {
				t.Errorf("theta_std_err = %.12g, R %.12g", res.ThetaStdErr, r.ThetaStdErr)
			}
		})
	}
}

// TestReferenceOracle_GLMNegBinResampleMatchesR: the leave-one-out,
// delete-a-cluster and frequency-weighted jackknife through the modifier
// engine, and the bootstrap over R's own index sets through the same
// replicate hook / summary / packaging the engine runs, re-estimate θ in
// every replicate (or hold the fixed θ) and reproduce base R's glm.nb
// replicate loop: the coefficients' replicate SE, θ̂'s replicate SE as
// theta_std_err (θ estimated only), and the ame / mem effects' replicate
// SE — each replicate's effect read at its own θ̂ family.
func TestReferenceOracle_GLMNegBinResampleMatchesR(t *testing.T) {
	t.Parallel()

	doc := loadGLMNBRefit(t)
	recs := doc.records()
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			spec := glmNBRefitSpec([]string{"x1", "x2"}, c.ThetaFixed)
			spec.Resample = c.Resample
			if c.By {
				spec.JackknifeBy = "g"
			}
			if c.Weighted {
				spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
			}
			if c.Margins != "" {
				spec.Margins = &types.RegressionMargins{Type: c.Margins}
			}
			var res *types.RegressionResult
			if c.Resample == "jackknife" {
				var err error
				if res, err = fitModifier(t, spec, recs); err != nil {
					t.Fatalf("fit: %v", err)
				}
			} else {
				base := fitPlain(t, spec, recs)
				eng, err := newModifierEngine(spec, nil)
				if err != nil {
					t.Fatal(err)
				}
				me := eng.(*modifierEngine)
				fitter, pv := me.makeResampleFitter(spec.Predictors)
				derive := withThetaDerive(spec, marginsDerive(spec, true, me.glmFam, spec.Predictors, base.Margins))
				rep := refitReplicate(2, fitter, derive)
				reps := make([]replicate, 0, len(c.Samples))
				for _, idx := range c.Samples {
					sample := make([]Record, len(idx))
					for i, r := range idx {
						sample[i] = recs[r]
					}
					r, err := rep(sample)
					if err != nil {
						t.Fatalf("replicate: %v", err)
					}
					reps = append(reps, r)
				}
				div := float64(len(reps) - 1)
				run := newReplicateRun(reps, 3, false, true, func(s float64) float64 { return s / div })
				res = packageResampleResult(spec, base, spec.Predictors, run, pv, nil)
			}
			for j, k := range []string{InterceptKey, "x1", "x2"} {
				if !relNear(res.Coefficients[k], c.Coefficients[j], 1e-7) {
					t.Errorf("coefficient %s = %.12g, R %.12g", k, res.Coefficients[k], c.Coefficients[j])
				}
				if !relNear(res.StdErrors[k], c.CoefStdErrors[j], 1e-6) {
					t.Errorf("coefficient se %s = %.12g, R %.12g", k, res.StdErrors[k], c.CoefStdErrors[j])
				}
			}
			if !relNear(res.Theta, c.Theta, 1e-7) {
				t.Errorf("theta = %.12g, R %.12g", res.Theta, c.Theta)
			}
			if c.ThetaFixed > 0 {
				if res.ThetaStdErr != 0 {
					t.Errorf("fixed θ: theta_std_err %v, want absent", res.ThetaStdErr)
				}
			} else if !relNear(res.ThetaStdErr, c.ThetaStdErr, 1e-6) {
				t.Errorf("theta_std_err = %.12g, R replicate SE %.12g", res.ThetaStdErr, c.ThetaStdErr)
			}
			if c.Margins == "" {
				return
			}
			mg := res.Margins
			if mg == nil || mg.Inference != types.MarginsInferenceResample {
				t.Fatalf("margins %+v, want a resampled block", mg)
			}
			for j, term := range []string{"x1", "x2"} {
				if !relNear(mg.Estimates[term], c.MarginEstimates[j], 1e-7) {
					t.Errorf("margin %s = %.12g, R %.12g", term, mg.Estimates[term], c.MarginEstimates[j])
				}
				if !relNear(mg.StdErrors[term], c.MarginStdErrors[j], 1e-6) {
					t.Errorf("margin se %s = %.12g, R %.12g", term, mg.StdErrors[term], c.MarginStdErrors[j])
				}
			}
		})
	}
}
