package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// score_test.go pins the shared scoring kernel (score.go): a bound
// model's score equals the engine's own fitted values BIT FOR BIT — the
// GLM engine's converged η and μ = g⁻¹(η), read through the fitted test
// seam — and a terms subset sums exactly the listed terms in fit order.
// ATTR_REG_FITTED parity for the OLS family is pinned end to end in
// internal/processing (TestRegPredict_EqualsRegFittedBitForBit).

// scoreGLMRecords: 80 complete rows, two predictors, a 0/1 target and
// a count target, non-separable.
func scoreGLMRecords() []Record {
	out := make([]Record, 80)
	for i := range out {
		x1 := (float64(i) - 40) * 0.07
		x2 := math.Sin(0.37*float64(i) + 0.4)
		z := -0.3 + 0.9*x1 - 0.7*x2 + 1.1*math.Sin(1.9*float64(i))
		b := 0.0
		if z > 0 {
			b = 1
		}
		c := math.Round(math.Exp(0.4+0.25*x1+0.3*x2) + 0.6*math.Sin(0.71*float64(i)))
		if c < 0 {
			c = 0
		}
		out[i] = mockRecord{values: map[string]float64{"x1": x1, "x2": x2, "b": b, "c": c}}
	}
	return out
}

func mustBind(t *testing.T, spec *types.RegressionSpec, res *types.RegressionResult) *model.Bound {
	t.Helper()
	b, err := model.Bind("m", spec, res)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return b
}

func mustScorer(t *testing.T, b *model.Bound, terms []string, scale string) *Scorer {
	t.Helper()
	s, err := NewScorer(b, model.ScoreParams{Terms: terms, Scale: scale})
	if err != nil {
		t.Fatalf("NewScorer: %v", err)
	}
	return s
}

func sameBits(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

// TestScorer_GLMMatchesEngineFittedBitForBit: on the training rows a
// GLM score on the response scale is the engine's own fitted mean μ and
// on the link scale its linear predictor η, bit for bit, for each
// family / link — the coefficients travel through RegressionResult and
// model.Bind exactly as a compose fit binds them.
func TestScorer_GLMMatchesEngineFittedBitForBit(t *testing.T) {
	recs := scoreGLMRecords()
	for _, tc := range []struct{ family, link, target string }{
		{"binomial", "logit", "b"},
		{"poisson", "log", "c"},
	} {
		t.Run(tc.family+"_"+tc.link, func(t *testing.T) {
			spec := &types.RegressionSpec{Type: types.REG_GLM, Target: tc.target, Predictors: []string{"x1", "x2"}, Family: tc.family, Link: tc.link}
			eng := newGLM(t, spec).(*glmEngine)
			var eta, mu []float64
			eng.fitted = func(e, m []float64) {
				eta, mu = append([]float64(nil), e...), append([]float64(nil), m...)
			}
			res, err := eng.FitBuffered(recs)
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			if len(mu) != len(recs) {
				t.Fatalf("engine reported %d fitted values for %d rows", len(mu), len(recs))
			}
			b := mustBind(t, spec, res)
			resp := mustScorer(t, b, nil, "")
			link := mustScorer(t, b, nil, model.ScaleLink)
			for i, rec := range recs {
				got, ok := resp.Score(rec)
				if !ok || !sameBits(got, mu[i]) {
					t.Fatalf("row %d: response score %v (ok=%v), engine μ %v", i, got, ok, mu[i])
				}
				got, ok = link.Score(rec)
				if !ok || !sameBits(got, eta[i]) {
					t.Fatalf("row %d: link score %v (ok=%v), engine η %v", i, got, ok, eta[i])
				}
			}
			// The response scale is not the link scale on this fit.
			if r, _ := resp.Score(recs[0]); sameBits(r, eta[0]) {
				t.Fatalf("response score equals η on row 0 (%v): the inverse link was not applied", r)
			}
		})
	}
}

// TestScorer_BayesScoresPosteriorMean: a REG_BAYES_LINEAR fit scores
// intercept + Σ coefficient·x with the posterior means the result
// reports, at full precision.
func TestScorer_BayesScoresPosteriorMean(t *testing.T) {
	recs := scoreGLMRecords()
	spec := &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "c", Predictors: []string{"x1", "x2"}}
	res, err := newBayes(t, spec).FitBuffered(recs)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	s := mustScorer(t, mustBind(t, spec, res), nil, "")
	for i, rec := range recs {
		x1, _ := rec.NumericValue("x1")
		x2, _ := rec.NumericValue("x2")
		want := res.Coefficients[model.InterceptKey]
		want += res.Coefficients["x1"] * x1
		want += res.Coefficients["x2"] * x2
		if got, ok := s.Score(rec); !ok || !sameBits(got, want) {
			t.Fatalf("row %d: score %v (ok=%v), posterior-mean prediction %v", i, got, ok, want)
		}
	}
}

// TestScorer_Terms: a terms subset sums exactly the listed terms, in
// fit order whatever order they are listed in; every term plus the
// intercept is the whole model bit for bit; the intercept is included
// only when listed; a subset reads only its own columns.
func TestScorer_Terms(t *testing.T) {
	recs := scoreGLMRecords()
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "c", Predictors: []string{"x1", "x2"}}
	res, err := newOLS(t, spec).FitBuffered(recs)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}
	b := mustBind(t, spec, res)
	whole := mustScorer(t, b, nil, "")
	every := mustScorer(t, b, []string{"x2", model.InterceptKey, "x1"}, "")
	x2Only := mustScorer(t, b, []string{"x2"}, "")
	interceptOnly := mustScorer(t, b, []string{model.InterceptKey}, "")
	if got := x2Only.Columns(); len(got) != 1 || got[0] != "x2" {
		t.Fatalf("x2-only columns = %v", got)
	}
	for i, rec := range recs {
		w, _ := whole.Score(rec)
		e, _ := every.Score(rec)
		if !sameBits(w, e) {
			t.Fatalf("row %d: every term %v != whole model %v", i, e, w)
		}
		x2, _ := rec.NumericValue("x2")
		if got, _ := x2Only.Score(rec); !sameBits(got, b.Coefficients[1]*x2) {
			t.Fatalf("row %d: x2 contribution %v, want %v", i, got, b.Coefficients[1]*x2)
		}
		if got, _ := interceptOnly.Score(rec); !sameBits(got, b.Intercept) {
			t.Fatalf("row %d: intercept-only %v, want %v", i, got, b.Intercept)
		}
	}
	// A subset never reads a column it does not score.
	noX1 := mockRecord{values: map[string]float64{"x2": 0.5}}
	if got, ok := x2Only.Score(noX1); !ok || !sameBits(got, b.Coefficients[1]*0.5) {
		t.Fatalf("x2 contribution on a row without x1 = %v (ok=%v)", got, ok)
	}
	if _, ok := whole.Score(noX1); ok {
		t.Fatal("whole-model score of a row missing x1 is not null")
	}
}

// TestScorer_NullColumnIsNull: a null (or absent) column the score
// reads makes the score null, never 0.
func TestScorer_NullColumnIsNull(t *testing.T) {
	b := &model.Bound{Name: "m", Type: types.REG_OLS, Predictors: []string{"a", "b"}, Coefficients: []float64{2, 3}, Intercept: 1}
	s := mustScorer(t, b, nil, "")
	if v, ok := s.Score(mockRecord{values: map[string]float64{"a": 1, "b": 2}}); !ok || v != 9 {
		t.Fatalf("complete row = %v (ok=%v), want 9", v, ok)
	}
	if _, ok := s.Score(mockRecord{values: map[string]float64{"a": 1, "b": 2}, nulls: map[string]bool{"b": true}}); ok {
		t.Fatal("a null predictor scored")
	}
}

// TestScorer_SelectionDroppedTermScoresZero: under Selection the bound
// predictors are the kept features; a candidate the selection dropped
// may still be listed in terms, contributes nothing and reads no column.
func TestScorer_SelectionDroppedTermScoresZero(t *testing.T) {
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"a", "b"}, Selection: "forward", Criterion: "aic"}
	res := &types.RegressionResult{Type: types.REG_OLS, SelectedFeatures: []string{"a"},
		Coefficients: map[string]float64{model.InterceptKey: 1, "a": 2}}
	b := mustBind(t, spec, res)
	s := mustScorer(t, b, []string{"b"}, "")
	if cols := s.Columns(); len(cols) != 0 {
		t.Fatalf("dropped-term scorer reads %v", cols)
	}
	if v, ok := s.Score(mockRecord{values: map[string]float64{}}); !ok || v != 0 {
		t.Fatalf("dropped term scored %v (ok=%v), want 0", v, ok)
	}
}
