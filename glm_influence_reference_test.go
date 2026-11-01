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

// glm_influence_reference_test.go pins the influence figures over a
// BOUND fit (U65 E1-S2) end to end: a Compose `fits` REG_OLS / REG_GLM
// entry with vcov: true, then params.fit slots scoring every row's
// ATTR_REG_LEVERAGE, ATTR_REG_RESIDUAL kind standardized / studentized
// and ATTR_REG_INFLUENCE measure cooks / dffits, each held to R 4.6.1's
// hatvalues / rstandard / rstudent / cooks.distance / dffits on the
// same rows (internal/processing/testdata/reference/glm_influence.json,
// scripts/reference/gen_glm_influence.R — make reference).

const glmInfluenceGolden = "internal/processing/testdata/reference/glm_influence.json"

type glmInfluenceCase struct {
	Name         string      `json:"name"`
	Family       string      `json:"family"`
	Link         string      `json:"link"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	Dispersion   []float64   `json:"dispersion"`
	Hat          []float64   `json:"hat"`
	Standardized []float64   `json:"standardized"`
	Studentized  []float64   `json:"studentized"`
	Cooks        []float64   `json:"cooks"`
	DFFITS       []float64   `json:"dffits"`
}

func loadGLMInfluence(t *testing.T) []glmInfluenceCase {
	t.Helper()
	raw, err := os.ReadFile(glmInfluenceGolden)
	if err != nil {
		t.Fatalf("%v (regenerate with make reference)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatalf("%s has no golden-hash footer", glmInfluenceGolden)
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatalf("%s: hash mismatch (hand-edited?); regenerate with make reference", glmInfluenceGolden)
	}
	var doc struct {
		Cases []glmInfluenceCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) != 5 {
		t.Fatalf("%d cases, want 5", len(doc.Cases))
	}
	return doc.Cases
}

// glmInfluencePulse writes a case's rows as glm.pulse {id, x1, x2, y}.
func glmInfluencePulse(t *testing.T, c glmInfluenceCase) *Pulse {
	t.Helper()
	rows := make([][]any, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = []any{float64(i), r[0], r[1], r[2]}
	}
	return strdPulse(t, "glm.pulse", []string{"id", "x1", "x2", "y"}, rows, nil)
}

// glmInfluenceSpec is the case's fit as Pulse runs it: REG_OLS for lm
// and the gaussian identity GLM (the engine fits Gaussian / identity as
// OLS; R's gaussian glm is lm), REG_GLM otherwise, with vcov.
func glmInfluenceSpec(c glmInfluenceCase) *types.RegressionSpec {
	if c.Family == "gaussian" {
		return &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}, Vcov: true}
	}
	return &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: c.Family, Link: c.Link, Target: "y", Predictors: []string{"x1", "x2"},
		Vcov: true, Tol: 1e-14, MaxIters: 200}
}

func glmInfluenceFits(reg *types.RegressionSpec) []types.FitSpec {
	return []types.FitSpec{{Name: "m", Request: &Request{Cohort: &types.Cohort{Filename: "glm.pulse"}, Regressions: []*types.RegressionSpec{reg}}}}
}

// glmInfluenceSlot scores the five figures through params.fit "m".
func glmInfluenceSlot() *Request {
	return fitRefSlot("glm.pulse", "id",
		fitRefAttr(types.ATTR_REG_LEVERAGE, "hat", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "standardized", `{"fit":"m","kind":"standardized"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "studentized", `{"fit":"m","kind":"studentized"}`),
		fitRefAttr(types.ATTR_REG_INFLUENCE, "cooks", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_INFLUENCE, "dffits", `{"fit":"m","measure":"dffits"}`),
	)
}

// relErr is |got − want| / max(|want|, 1e-12/1e-9): relative, with the
// absolute floor 1e-12.
func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Max(math.Abs(want), 1e-12/1e-9)
}

// TestGLMInfluence_MatchesR: every row's leverage, standardized and
// studentized residual, Cook's distance and DFFITS over a bound OLS,
// binomial-logit, poisson-log and gamma-inverse fit agree with R 4.6.1
// to 1e-9 relative (absolute floor 1e-12), and so does the fit's
// dispersion; the gaussian GLM case is lm's (the oracle's two cases
// agree, and Pulse matches both).
func TestGLMInfluence_MatchesR(t *testing.T) {
	t.Parallel()

	const tol = 1e-9
	cases := loadGLMInfluence(t)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p := glmInfluencePulse(t, c)
			out, err := p.Compose(context.Background(), &ComposedRequest{Fits: glmInfluenceFits(glmInfluenceSpec(c)), Requests: []*Request{glmInfluenceSlot()}})
			if err != nil {
				t.Fatal(err)
			}
			fit := out.Fits["m"].RegressionResult
			for j, k := range []string{"(intercept)", "x1", "x2"} {
				if e := relErr(fit.Coefficients[k], c.Coefficients[j]); e > tol {
					t.Errorf("coefficient %s = %.17g, R %.17g (rel %.3g)", k, fit.Coefficients[k], c.Coefficients[j], e)
				}
			}
			phi := 1.0
			if c.Family == "gaussian" || c.Family == "gamma" {
				phi = fit.Dispersion
			} else if fit.Dispersion != 0 {
				t.Errorf("a %s fit carries dispersion %v; φ is fixed at 1", c.Family, fit.Dispersion)
			}
			if e := relErr(phi, c.Dispersion[0]); e > tol {
				t.Errorf("dispersion = %.17g, R %.17g (rel %.3g)", phi, c.Dispersion[0], e)
			}
			data := out.Responses[0].Data
			if len(data) != len(c.Rows) {
				t.Fatalf("%d rows, want %d", len(data), len(c.Rows))
			}
			want := map[string][]float64{"hat": c.Hat, "standardized": c.Standardized, "studentized": c.Studentized, "cooks": c.Cooks, "dffits": c.DFFITS}
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
		})
	}
	t.Run("gaussian GLM is lm", func(t *testing.T) {
		t.Parallel()
		var ols, gauss glmInfluenceCase
		for _, c := range cases {
			switch c.Name {
			case "ols":
				ols = c
			case "gaussian_identity":
				gauss = c
			}
		}
		pairs := [][2][]float64{{ols.Hat, gauss.Hat}, {ols.Standardized, gauss.Standardized}, {ols.Studentized, gauss.Studentized},
			{ols.Cooks, gauss.Cooks}, {ols.DFFITS, gauss.DFFITS}, {ols.Coefficients, gauss.Coefficients}, {ols.Dispersion, gauss.Dispersion}}
		for k, pr := range pairs {
			if len(pr[0]) != len(pr[1]) || len(pr[0]) == 0 {
				t.Fatalf("figure %d: lengths %d / %d", k, len(pr[0]), len(pr[1]))
			}
			for i := range pr[0] {
				if e := relErr(pr[1][i], pr[0][i]); e > 1e-9 {
					t.Errorf("figure %d row %d: gaussian glm %.17g, lm %.17g", k, i, pr[1][i], pr[0][i])
				}
			}
		}
	})
}

// TestGLMInfluence_LoadedModelMatchesFit: a vcov fit saved as a model
// file (model_format_version 4 where φ is estimated: it carries the
// dispersion) and loaded by a `models` entry scores every influence
// figure bit-equal to the in-request fit; a version-1 file of the same
// OLS fit — one an earlier build wrote, vcov but no dispersion — is
// refused (requires dispersion) at runtime and by predict alike.
func TestGLMInfluence_LoadedModelMatchesFit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, c := range loadGLMInfluence(t) {
		if c.Name != "ols" && c.Name != "gamma_inverse" && c.Name != "binomial_logit" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			p := glmInfluencePulse(t, c)
			fits := glmInfluenceFits(glmInfluenceSpec(c))
			fits[0].Save = "m.pulse-model.json"
			in, err := p.Compose(ctx, &ComposedRequest{Fits: fits, Requests: []*Request{glmInfluenceSlot()}})
			if err != nil {
				t.Fatal(err)
			}
			loaded := glmInfluenceSlot()
			loaded.Models = []types.ModelRef{{Name: "m", Path: "m.pulse-model.json"}}
			out, err := p.Process(ctx, loaded)
			if err != nil {
				t.Fatal(err)
			}
			for i, row := range out.Data {
				for _, col := range []string{"hat", "standardized", "studentized", "cooks", "dffits"} {
					if g, w := row[col], in.Responses[0].Data[i][col]; g != w {
						t.Errorf("row %d %s: loaded %v, in-request %v", i, col, g, w)
					}
				}
			}
		})
	}
	t.Run("version 1 file refused", func(t *testing.T) {
		t.Parallel()
		c := loadGLMInfluence(t)[0]
		p, fsys := matPulse(t, Options{})
		file := `{"model_format_version": 1, "name": "m", "type": "REG_OLS", "target": "y", "predictors": ["x1", "x2"], "terms": ["x1", "x2"],
			"coefficients": {"(intercept)": 1, "x1": 0.1, "x2": 0.2}, "n_obs": 40, "provenance": {},
			"vcov": {"kind": "covariance", "encoding": "full", "row_keys": ["(intercept)", "x1", "x2"], "column_keys": ["(intercept)", "x1", "x2"],
				"values": [[1, 0, 0], [0, 1, 0], [0, 0, 1]]}}`
		if err := afero.WriteFile(fsys, "old.pulse-model.json", []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		var schema encoding.Schema
		for _, name := range []string{"id", "x1", "x2", "y"} {
			schema.Fields = append(schema.Fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: "influence refusal column " + name})
		}
		b, err := p.NewCohortBuilder(ctx, "glm.pulse", schema, CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range c.Rows {
			if err := b.Append(CohortRow{float64(i), r[0], r[1], r[2]}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
		req := func() *Request {
			r := fitRefSlot("glm.pulse", "id", fitRefAttr(types.ATTR_REG_INFLUENCE, "d", `{"fit":"m"}`))
			r.Models = []types.ModelRef{{Name: "m", Path: "old.pulse-model.json"}}
			return r
		}
		_, err = p.Process(ctx, req())
		ce := codedOf(t, err)
		if ce.Details["requires"] != "dispersion" {
			t.Fatalf("Process err = %v, want requires dispersion", err)
		}
		pr, perr := p.Predict(ctx, req())
		if perr != nil {
			t.Fatal(perr)
		}
		matched := false
		for _, e := range pr.Errors {
			matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
		}
		if pr.Valid || !matched {
			t.Errorf("predict errors %+v do not carry the runtime refusal %q", pr.Errors, ce.Message)
		}
	})
}

// TestGLMInfluence_RefusalsMatchPredict: a design-reading params.fit
// slot over a fit that carries no such design — no vcov, a robust
// covariance, a penalty, selection, resample, a weight, a
// Bayesian-linear fit — is refused at runtime (Compose) and by
// PredictCompose with the same code and message, details.requires
// naming what is missing; the response residual and the fitted value
// over the same fit keep working.
func TestGLMInfluence_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := loadGLMInfluence(t)[3] // poisson_log
	rows := make([][]any, len(c.Rows))
	for i, r := range c.Rows {
		rows[i] = []any{float64(i), r[0], r[1], r[2], 1 + float64(i%3)}
	}
	p := strdPulse(t, "glm.pulse", []string{"id", "x1", "x2", "y", "w"}, rows, nil)
	glm := func(edit func(*types.RegressionSpec)) *types.RegressionSpec {
		r := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "poisson", Target: "y", Predictors: []string{"x1", "x2"}, Vcov: true}
		edit(r)
		return r
	}
	ols := func(edit func(*types.RegressionSpec)) *types.RegressionSpec {
		r := &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}, Vcov: true}
		edit(r)
		return r
	}
	weight := types.SlotWeightField("w")
	slots := []*types.Attribute{
		fitRefAttr(types.ATTR_REG_LEVERAGE, "a", `{"fit":"m"}`),
		fitRefAttr(types.ATTR_REG_INFLUENCE, "a", `{"fit":"m","measure":"dffits"}`),
		fitRefAttr(types.ATTR_REG_RESIDUAL, "a", `{"fit":"m","kind":"studentized"}`),
	}
	for _, tc := range []struct {
		name     string
		reg      *types.RegressionSpec
		reason   string
		requires string
	}{
		{"no vcov", glm(func(r *types.RegressionSpec) { r.Vcov = false }), "params_invalid", "vcov"},
		{"no vcov ols", ols(func(r *types.RegressionSpec) { r.Vcov = false }), "params_invalid", "vcov"},
		{"robust covariance", ols(func(r *types.RegressionSpec) { r.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC3} }), "params_invalid", "classical_covariance"},
		{"penalized", glm(func(r *types.RegressionSpec) { r.Penalty, r.Alpha = "l2", 0.1 }), "params_invalid", "unpenalized"},
		{"selection", ols(func(r *types.RegressionSpec) { r.Selection, r.Criterion = "forward", "aic" }), "params_invalid", "no_selection"},
		{"resample", ols(func(r *types.RegressionSpec) { r.Resample = "jackknife" }), "params_invalid", "no_resample"},
		{"weighted", glm(func(r *types.RegressionSpec) { r.Weight = weight }), "params_invalid", "unweighted"},
		{"bayes", &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}, Vcov: true}, "type_unsupported", ""},
	} {
		for _, slot := range slots {
			t.Run(tc.name+"/"+string(slot.Type), func(t *testing.T) {
				t.Parallel()
				req := func() *ComposedRequest {
					return &ComposedRequest{Fits: glmInfluenceFits(tc.reg), Requests: []*Request{fitRefSlot("glm.pulse", "id", slot)}}
				}
				_, err := p.Compose(ctx, req())
				ce := codedOf(t, err)
				if ce.Code != errors.PULSE_MODEL_SCORE_INVALID || ce.Details["reason"] != tc.reason {
					t.Fatalf("Compose err = %v (details %v), want PULSE_MODEL_SCORE_INVALID %s", err, ce.Details, tc.reason)
				}
				if tc.requires != "" && ce.Details["requires"] != tc.requires {
					t.Fatalf("details.requires = %v, want %s", ce.Details["requires"], tc.requires)
				}
				env, perr := p.PredictCompose(ctx, req())
				if perr != nil {
					t.Fatal(perr)
				}
				matched := false
				for _, e := range env.Errors {
					matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
				}
				if !matched {
					t.Errorf("predict errors %+v do not carry the runtime refusal %s %q", env.Errors, ce.Code, ce.Message)
				}
			})
		}
	}
	t.Run("default weight", func(t *testing.T) {
		t.Parallel()
		// A fit whose regression names no weight inherits
		// Options.DefaultWeight: weighted, refused alike; weight: null
		// opts it out.
		dp, _ := matPulse(t, Options{DefaultWeight: &types.WeightSpec{Field: "w"}})
		var schema encoding.Schema
		for _, name := range []string{"id", "x1", "x2", "y", "w"} {
			schema.Fields = append(schema.Fields, encoding.Field{Name: name, Type: encoding.FieldTypeF64, Description: "influence refusal column " + name})
		}
		b, err := dp.NewCohortBuilder(ctx, "glm.pulse", schema, CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if err := b.Append(CohortRow(r)); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
		req := func(reg *types.RegressionSpec) *ComposedRequest {
			return &ComposedRequest{Fits: glmInfluenceFits(reg), Requests: []*Request{fitRefSlot("glm.pulse", "id", fitRefAttr(types.ATTR_REG_LEVERAGE, "h", `{"fit":"m"}`))}}
		}
		_, err = dp.Compose(ctx, req(glm(func(*types.RegressionSpec) {})))
		ce := codedOf(t, err)
		if ce.Details["requires"] != "unweighted" {
			t.Fatalf("Compose err = %v, want requires unweighted", err)
		}
		env, perr := dp.PredictCompose(ctx, req(glm(func(*types.RegressionSpec) {})))
		if perr != nil {
			t.Fatal(perr)
		}
		matched := false
		for _, e := range env.Errors {
			matched = matched || (e.Code == string(ce.Code) && e.Message == ce.Message)
		}
		if !matched {
			t.Errorf("predict errors %+v do not carry the runtime refusal %q", env.Errors, ce.Message)
		}
		unweighted := glm(func(r *types.RegressionSpec) { r.Weight = types.NullSlotWeight() })
		if _, err := dp.Compose(ctx, req(unweighted)); err != nil {
			t.Fatalf("weight: null fit: %v", err)
		}
		if env, _ := dp.PredictCompose(ctx, req(unweighted)); len(env.Errors) != 0 {
			t.Fatalf("predict refuses the weight: null fit: %+v", env.Errors)
		}
	})
	t.Run("weighted fit keeps the response residual and fitted value", func(t *testing.T) {
		t.Parallel()
		_, err := p.Compose(ctx, &ComposedRequest{Fits: glmInfluenceFits(glm(func(r *types.RegressionSpec) { r.Weight = weight })),
			Requests: []*Request{fitRefSlot("glm.pulse", "id",
				fitRefAttr(types.ATTR_REG_FITTED, "f", `{"fit":"m"}`),
				fitRefAttr(types.ATTR_REG_RESIDUAL, "r", `{"fit":"m","kind":"pearson"}`))}})
		if err != nil {
			t.Fatal(err)
		}
	})
}
