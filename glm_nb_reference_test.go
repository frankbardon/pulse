package pulse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// glm_nb_reference_test.go pins the REG_GLM negative binomial family
// (NB2, U65 E2-S2) end to end over a BOUND fit: a Compose `fits`
// negative_binomial entry (θ estimated on the log / sqrt / identity
// links, or fixed) with vcov, then params.fit slots scoring every row's
// ATTR_REG_FITTED / ATTR_REG_PREDICT, ATTR_REG_RESIDUAL kinds pearson /
// deviance / working (V(μ) = μ + μ²/θ̂ and the NB unit deviance),
// ATTR_REG_LEVERAGE and, on the influence case, the standardized /
// studentized residuals and ATTR_REG_INFLUENCE cooks / dffits at
// dispersion 1 — each held to R 4.6.1 + MASS 7.3-66
// (internal/processing/testdata/reference/glm_nb.json,
// scripts/reference/gen_glm_nb.R — make reference). The model file
// saved from the fit carries θ (model_format_version 5) and scores the
// same rows bit for bit when loaded. Fit-level figures:
// TestReferenceOracle_GLMNegBinMatchesR (internal/processing/regression).

const glmNBGolden = "internal/processing/testdata/reference/glm_nb.json"

type glmNBRefCase struct {
	Name                 string      `json:"name"`
	Link                 string      `json:"link"`
	ThetaFixed           bool        `json:"theta_fixed"`
	Weighted             bool        `json:"weighted"`
	DispersionMethod     string      `json:"dispersion_method"`
	Dispersion           []float64   `json:"dispersion"`
	Rows                 [][]float64 `json:"rows"`
	Coefficients         []float64   `json:"coefficients"`
	Theta                []float64   `json:"theta"`
	Fitted               []float64   `json:"fitted"`
	Pearson              []float64   `json:"pearson"`
	DevianceResiduals    []float64   `json:"deviance_residuals"`
	Working              []float64   `json:"working"`
	Hat                  []float64   `json:"hat"`
	Standardized         []float64   `json:"standardized"`
	StandardizedDeviance []float64   `json:"standardized_deviance"`
	Studentized          []float64   `json:"studentized"`
	Cooks                []float64   `json:"cooks"`
	DFFITS               []float64   `json:"dffits"`
}

func loadGLMNBRef(t *testing.T) []glmNBRefCase {
	t.Helper()
	raw, err := os.ReadFile(glmNBGolden)
	if err != nil {
		t.Fatalf("%v (regenerate with make reference)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatalf("%s has no golden-hash footer", glmNBGolden)
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatalf("%s: hash mismatch (hand-edited?); regenerate with make reference", glmNBGolden)
	}
	var doc struct {
		Cases []glmNBRefCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	var out []glmNBRefCase
	for _, c := range doc.Cases {
		if !c.Weighted {
			out = append(out, c)
		}
	}
	if len(out) != 7 {
		t.Fatalf("%d unweighted cases, want 7", len(out))
	}
	return out
}

// glmNBRefPulse writes a case's rows as glm.pulse {id, x1, x2, y} on a
// memory fs it returns too (the saved model file is read back).
func glmNBRefPulse(t *testing.T, rows [][]float64) (*Pulse, afero.Fs) {
	t.Helper()
	p, fsys := matPulse(t, Options{})
	var schema encoding.Schema
	for _, name := range []string{"id", "x1", "x2", "y"} {
		schema.Fields = append(schema.Fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: "negative binomial oracle column " + name})
	}
	b, err := p.NewCohortBuilder(context.Background(), "glm.pulse", schema, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if err := b.Append(CohortRow{float64(i), r[0], r[1], r[2]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func glmNBRefSpec(c glmNBRefCase) *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "negative_binomial", Link: c.Link,
		Target: "y", Predictors: []string{"x1", "x2"}, Vcov: true, Tol: 1e-13, MaxIters: 500}
	if c.ThetaFixed {
		s.Theta = c.Theta[0]
	}
	s.Dispersion = c.DispersionMethod
	return s
}

func glmNBRefSlot() *Request {
	return fitRefSlot("glm.pulse", "id",
		fitRefAttr(types.ATTR_REG_FITTED, "fitted", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_PREDICT, "predict", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "pearson", `{"fit":"m","kind":"pearson"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "deviance", `{"fit":"m","kind":"deviance"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "working", `{"fit":"m","kind":"working"}`),
		fitRefAttr(types.ATTR_REG_LEVERAGE, "hat", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "standardized", `{"fit":"m","kind":"standardized"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "standardized_deviance", `{"fit":"m","kind":"standardized","residual_type":"deviance"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "studentized", `{"fit":"m","kind":"studentized"}`),
		fitRefAttr(types.ATTR_REG_INFLUENCE, "cooks", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_INFLUENCE, "dffits", `{"fit":"m","measure":"dffits"}`),
	)
}

// TestGLMNegBin_BoundFitMatchesR: over a bound negative binomial fit
// (θ estimated or fixed), every row's fitted mean (its ATTR_REG_PREDICT
// score bit for bit), Pearson / deviance / working residual and
// leverage agree with R at 1e-9 relative (absolute floor 1e-12), and on
// the influence case so do the standardized (Pearson and deviance) and
// studentized residuals, Cook's distance and DFFITS at dispersion 1
// (rstandard / cooks.distance on the negbin fit; rstudent / dffits with
// family$dispersion = 1) — or, on a fixed-θ fit under dispersion
// "estimated", at summary.glm's φ̂ with rstudent / dffits on the
// leave-one-out σ (R on the bare glm(negative.binomial(θ)) fit). A model
// file saved from the fit is model_format_version 5 carrying θ̂ (6 with
// its φ̂ too), and the loaded model scores every column bit for bit as
// the in-request fit does.
func TestGLMNegBin_BoundFitMatchesR(t *testing.T) {
	t.Parallel()

	const tol = 1e-9
	ctx := context.Background()
	for _, c := range loadGLMNBRef(t) {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p, fsys := glmNBRefPulse(t, c.Rows)
			fits := glmInfluenceFits(glmNBRefSpec(c))
			fits[0].Save = "m.pulse-model.json"
			out, err := p.Compose(ctx, &ComposedRequest{Fits: fits, Requests: []*Request{glmNBRefSlot()}})
			if err != nil {
				t.Fatal(err)
			}
			fit := out.Fits["m"].RegressionResult
			if fit.Family != "negative_binomial" || fit.Link != c.Link {
				t.Fatalf("fit echoes %s/%s", fit.Family, fit.Link)
			}
			if e := relErr(fit.Theta, c.Theta[0]); e > tol {
				t.Errorf("θ = %.17g, R %.17g (rel %.3g)", fit.Theta, c.Theta[0], e)
			}
			for j, k := range []string{"(intercept)", "x1", "x2"} {
				if e := relErr(fit.Coefficients[k], c.Coefficients[j]); e > tol {
					t.Errorf("coefficient %s = %.17g, R %.17g (rel %.3g)", k, fit.Coefficients[k], c.Coefficients[j], e)
				}
			}
			data := out.Responses[0].Data
			if len(data) != len(c.Rows) {
				t.Fatalf("%d rows, want %d", len(data), len(c.Rows))
			}
			want := map[string][]float64{"fitted": c.Fitted, "pearson": c.Pearson, "deviance": c.DevianceResiduals, "working": c.Working, "hat": c.Hat}
			if c.Cooks != nil {
				want["standardized"], want["standardized_deviance"], want["studentized"] = c.Standardized, c.StandardizedDeviance, c.Studentized
				want["cooks"], want["dffits"] = c.Cooks, c.DFFITS
			}
			for col, ws := range want {
				worst := 0.0
				for i, row := range data {
					got, ok := row[col].(float64)
					if !ok {
						t.Fatalf("row %d %s = %v, not a number", i, col, row[col])
					}
					e := relErr(got, ws[i])
					worst = math.Max(worst, e)
					if e > tol {
						t.Errorf("row %d %s = %.17g, R %.17g (rel %.3g)", i, col, got, ws[i], e)
					}
				}
				t.Logf("%s %s: max rel error %.3g", c.Name, col, worst)
			}
			for i, row := range data {
				if row["fitted"] != row["predict"] {
					t.Errorf("row %d: fitted %v ≠ ATTR_REG_PREDICT %v", i, row["fitted"], row["predict"])
				}
			}

			// The model file carries θ (version 5; 6 with its estimated
			// dispersion φ̂ beside it) and scores bit for bit.
			raw, err := fsys.Open("m.pulse-model.json")
			if err != nil {
				t.Fatal(err)
			}
			var file struct {
				Version int     `json:"model_format_version"`
				Family  string  `json:"family"`
				Theta   float64 `json:"theta"`
				Disp    float64 `json:"dispersion"`
			}
			if err := json.NewDecoder(raw).Decode(&file); err != nil {
				t.Fatal(err)
			}
			_ = raw.Close()
			wantVersion, wantDisp := 5, 0.0
			if c.DispersionMethod == types.NegBinDispersionEstimated {
				wantVersion, wantDisp = 6, fit.Dispersion
				if e := relErr(fit.Dispersion, c.Dispersion[0]); e > tol {
					t.Errorf("dispersion = %.17g, R %.17g (rel %.3g)", fit.Dispersion, c.Dispersion[0], e)
				}
			}
			if file.Version != wantVersion || file.Family != "negative_binomial" || file.Theta != fit.Theta || file.Disp != wantDisp {
				t.Errorf("model file version %d family %s θ %v φ %v, fit θ %v φ %v", file.Version, file.Family, file.Theta, file.Disp, fit.Theta, wantDisp)
			}
			loaded := glmNBRefSlot()
			loaded.Models = []types.ModelRef{{Name: "m", Path: "m.pulse-model.json"}}
			lo, err := p.Process(ctx, loaded)
			if err != nil {
				t.Fatal(err)
			}
			for i, row := range lo.Data {
				for col := range data[i] {
					if g, w := row[col], data[i][col]; g != w {
						t.Errorf("row %d %s: loaded %v, in-request %v", i, col, g, w)
					}
				}
			}
		})
	}
}

// TestGLMNegBin_PredictMatchesRuntime: every negative binomial refusal —
// theta off the family (REG_GLM poisson, REG_OLS), theta ≤ 0, a
// penalty, selection or resample on the family, and dispersion off the
// family, with θ estimated or of an unknown value — is the same
// PROCESSING_CONFIG code, message and details.requires at predict and
// at run; a valid request (θ estimated or fixed, φ fixed or estimated
// with a fixed θ, classical or robust covariance, weighted) passes predict and runs.
func TestGLMNegBin_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := loadGLMNBRef(t)[0]
	p, _ := glmNBRefPulse(t, c.Rows)
	req := func(edit func(*types.RegressionSpec)) *Request {
		reg := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "negative_binomial", Target: "y", Predictors: []string{"x1", "x2"}}
		edit(reg)
		return &Request{Cohort: &types.Cohort{Filename: "glm.pulse"}, Regressions: []*types.RegressionSpec{reg}}
	}
	for name, edit := range map[string]func(*types.RegressionSpec){
		"estimated": func(*types.RegressionSpec) {},
		"fixed":     func(r *types.RegressionSpec) { r.Theta = 2 },
		"fixed φ̂":  func(r *types.RegressionSpec) { r.Theta, r.Dispersion = 2, types.NegBinDispersionEstimated },
		"fixed φ=1": func(r *types.RegressionSpec) { r.Theta, r.Dispersion = 2, types.NegBinDispersionFixed },
		"sqrt":      func(r *types.RegressionSpec) { r.Link = "sqrt" },
		"robust":    func(r *types.RegressionSpec) { r.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3} },
		"weighted": func(r *types.RegressionSpec) {
			r.Weight = types.SlotWeightOf(types.WeightSpec{Field: "x1", Kind: types.WeightKindProbability})
		},
	} {
		pr, err := p.Predict(ctx, req(edit))
		if err != nil {
			t.Fatal(err)
		}
		if !pr.Valid {
			t.Errorf("%s: predict refuses %+v", name, pr.Errors)
		}
		res, err := p.Process(ctx, req(edit))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r := res.Regressions[0]; r.Error != nil || !(r.Theta > 0) {
			t.Fatalf("%s: slot failed %v (θ %v)", name, r.Error, r.Theta)
		}
	}
	for _, tc := range []struct {
		name     string
		edit     func(*types.RegressionSpec)
		requires string
	}{
		{"theta on poisson", func(r *types.RegressionSpec) { r.Family, r.Theta = "poisson", 2 }, "family negative_binomial"},
		{"theta on REG_OLS", func(r *types.RegressionSpec) { r.Type, r.Family, r.Theta = types.REG_OLS, "", 2 }, "family negative_binomial"},
		{"negative theta", func(r *types.RegressionSpec) { r.Theta = -1 }, "theta > 0"},
		{"penalty", func(r *types.RegressionSpec) { r.Penalty, r.Alpha = "l2", 0.1 }, "unpenalized"},
		{"selection", func(r *types.RegressionSpec) { r.Selection, r.Criterion = "forward", "aic" }, "no_selection"},
		{"resample", func(r *types.RegressionSpec) { r.Resample = "jackknife" }, "no_resample"},
		{"dispersion on poisson", func(r *types.RegressionSpec) { r.Family, r.Dispersion = "poisson", types.NegBinDispersionEstimated }, "family negative_binomial"},
		{"dispersion on REG_OLS", func(r *types.RegressionSpec) {
			r.Type, r.Family, r.Dispersion = types.REG_OLS, "", types.NegBinDispersionFixed
		}, "family negative_binomial"},
		{"dispersion with θ estimated", func(r *types.RegressionSpec) { r.Dispersion = types.NegBinDispersionEstimated }, "theta"},
		{"unknown dispersion", func(r *types.RegressionSpec) { r.Theta, r.Dispersion = 2, "quasi" }, "dispersion fixed or estimated"},
	} {
		_, err := p.Process(ctx, req(tc.edit))
		ce := codedOf(t, err)
		if ce.Code != errors.PROCESSING_CONFIG || ce.Details["requires"] != tc.requires {
			t.Fatalf("%s: Process err %v (details %v), want PROCESSING_CONFIG requires %q", tc.name, err, ce.Details, tc.requires)
		}
		pr, perr := p.Predict(ctx, req(tc.edit))
		if perr != nil {
			t.Fatal(perr)
		}
		matched := false
		for _, e := range pr.Errors {
			if e.Code == string(ce.Code) && e.Message == ce.Message && e.Details["requires"] == tc.requires {
				matched = true
			}
		}
		if pr.Valid || !matched {
			t.Errorf("%s: predict errors %+v do not carry the runtime refusal %q", tc.name, pr.Errors, ce.Message)
		}
	}
}
