package regression

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// bayesPrecisionCase is one bayes_precision.json case
// (scripts/reference/gen_bayes_precision.R): the NIG posterior under a
// per-coefficient prior precision Λ₀ = diag(v), intercept first.
type bayesPrecisionCase struct {
	Name           string      `json:"name"`
	Predictors     []string    `json:"predictors"`
	Weighted       bool        `json:"weighted"`
	Precision      []float64   `json:"precision"`
	Mu0            []float64   `json:"mu0"`
	Mu0Set         bool        `json:"mu0_set"`
	Rows           [][]float64 `json:"rows"`
	NObs           float64     `json:"n_obs"`
	DF             float64     `json:"df"`
	Coefficients   []float64   `json:"coefficients"`
	StdErrors      []float64   `json:"std_errors"`
	CredibleLower  []float64   `json:"credible_lower"`
	CredibleUpper  []float64   `json:"credible_upper"`
	ResidualStdErr float64     `json:"residual_std_err"`
	R2             float64     `json:"r2"`
	AdjR2          float64     `json:"adj_r2"`
	Vcov           [][]float64 `json:"vcov"`
}

func loadBayesPrecisionReference(t *testing.T) []bayesPrecisionCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "bayes_precision.json"))
	if err != nil {
		t.Fatalf("read bayes_precision.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		RVersion string               `json:"r_version"`
		Cases    []bayesPrecisionCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("decode bayes_precision.json: %v", err)
	}
	if doc.RVersion == "" || len(doc.Cases) == 0 {
		t.Fatal("bayes_precision.json has no R version or no cases")
	}
	return doc.Cases
}

func (c bayesPrecisionCase) records() []Record {
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

func (c bayesPrecisionCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{
		Type: types.REG_BAYES_LINEAR, Name: c.Name, Target: "y", Predictors: c.Predictors, Vcov: true,
		PriorPrecision: types.PrecisionVector(c.Precision),
	}
	if c.Mu0Set {
		s.PriorMu = c.Mu0
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return s
}

// TestReferenceOracle_RegBayesPrecisionVectorMatchesR: a per-coefficient
// prior_precision — flat (0) entries, an experiment-centred strong entry,
// a pinning one, plain and frequency-weighted — reports R's conjugate
// posterior under Λ₀ = diag(v): mean, marginal t scale, credible
// interval, df, σ scale, R², adjusted R² and posterior covariance, every
// figure within relative 1e-10.
func TestReferenceOracle_RegBayesPrecisionVectorMatchesR(t *testing.T) {
	const tol = 1e-10
	var sawFlat, sawWeighted bool
	for _, c := range loadBayesPrecisionReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			sawWeighted = sawWeighted || c.Weighted
			for _, v := range c.Precision {
				sawFlat = sawFlat || v == 0
			}
			res, err := newBayes(t, c.spec()).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			check := func(what string, got, want float64) {
				t.Helper()
				if !refRelClose(got, want, tol) {
					t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want,
						math.Abs(got-want)/math.Max(math.Abs(got), math.Abs(want)))
				}
			}
			keys := coefficientKeys(c.Predictors)
			for j, key := range keys {
				check("β["+key+"]", res.Coefficients[key], c.Coefficients[j])
				check("SE["+key+"]", res.StdErrors[key], c.StdErrors[j])
				check("credible_lower["+key+"]", res.CredibleIntervals[key][0], c.CredibleLower[j])
				check("credible_upper["+key+"]", res.CredibleIntervals[key][1], c.CredibleUpper[j])
			}
			check("df", res.DF, c.DF)
			check("residual_std_err", res.ResidualStdErr, c.ResidualStdErr)
			check("r2", res.R2, c.R2)
			check("adj_r2", res.AdjR2, c.AdjR2)
			if res.Vcov == nil || len(res.Vcov.Values) != len(keys) {
				t.Fatalf("vcov %+v, want %d×%d", res.Vcov, len(keys), len(keys))
			}
			for i := range keys {
				for j := range keys {
					check("vcov["+keys[i]+"]["+keys[j]+"]", res.Vcov.Values[i][j], c.Vcov[i][j])
				}
			}
		})
	}
	if !sawFlat || !sawWeighted {
		t.Fatalf("golden must carry a flat-entry and a weighted case (flat %v, weighted %v)", sawFlat, sawWeighted)
	}
}

// precisionFixture is a two-predictor design for the property tests.
func precisionFixture() []Record {
	recs := make([]Record, 30)
	for i := range recs {
		fi := float64(i)
		x1 := 3 + 2*math.Sin(0.31*fi)
		x2 := math.Cos(0.17*fi) + fi/20
		y := 2.5 + float64(0.9*x1) - float64(1.4*x2) + float64(1.2*math.Sin(1.9*fi+0.4))
		recs[i] = mockRecord{values: map[string]float64{"x1": x1, "x2": x2, "y": y}}
	}
	return recs
}

// TestRegBayes_ScalarPrecisionEqualsUniformVector: the scalar ε and the
// vector [ε, ε, ε] are one prior. The posterior mean is bit identical
// (every Λ₀ term enters Λ_n and its right-hand side the same way); b_n is
// not: the scalar path keeps the historical quadratic form, the vector
// path the cancellation-free residual form b₀ + ½(RSS + ΔᵀΛ₀Δ), so the
// scale figures (standard errors, intervals, residual SE, vcov) agree
// within a few ulps.
func TestRegBayes_ScalarPrecisionEqualsUniformVector(t *testing.T) {
	recs := precisionFixture()
	for _, mu := range [][]float64{nil, {0.5, 1.1, -0.7}} {
		scalar := &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x1", "x2"},
			PriorMu: mu, PriorPrecision: types.PrecisionScalar(0.75), Vcov: true}
		vector := *scalar
		vector.PriorPrecision = types.PrecisionVector([]float64{0.75, 0.75, 0.75})
		a, err := newBayes(t, scalar).FitBuffered(recs)
		if err != nil {
			t.Fatal(err)
		}
		b, err := newBayes(t, &vector).FitBuffered(recs)
		if err != nil {
			t.Fatal(err)
		}
		if mu == nil && !reflect.DeepEqual(a.Coefficients, b.Coefficients) {
			t.Errorf("μ₀ = 0: scalar β %v, vector β %v", a.Coefficients, b.Coefficients)
		}
		if !refRelClose(a.ResidualStdErr, b.ResidualStdErr, 1e-12) {
			t.Errorf("residual SE: scalar %v vector %v", a.ResidualStdErr, b.ResidualStdErr)
		}
		for _, key := range coefficientKeys(scalar.Predictors) {
			if !refRelClose(a.Coefficients[key], b.Coefficients[key], 1e-14) || !refRelClose(a.StdErrors[key], b.StdErrors[key], 1e-12) ||
				!refRelClose(a.CILower[key], b.CILower[key], 1e-12) {
				t.Errorf("%s: scalar (%v, %v) vector (%v, %v)", key, a.Coefficients[key], a.StdErrors[key], b.Coefficients[key], b.StdErrors[key])
			}
		}
	}
}

// TestRegBayes_PrecisionToInfinityPinsPriorMean: as one coefficient's
// precision grows the posterior mean moves monotonically onto its prior
// mean and, at 1e12, sits on it; the coefficients with a weak prior keep
// fitting the data.
func TestRegBayes_PrecisionToInfinityPinsPriorMean(t *testing.T) {
	recs := precisionFixture()
	const target = -0.25
	prevGap := math.Inf(1)
	for _, lam := range []float64{1e-3, 1, 1e3, 1e6, 1e12} {
		spec := &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x1", "x2"},
			PriorMu: []float64{0, 0, target}, PriorPrecision: types.PrecisionVector([]float64{1e-3, 1e-3, lam})}
		res, err := newBayes(t, spec).FitBuffered(recs)
		if err != nil {
			t.Fatalf("λ=%g: %v", lam, err)
		}
		gap := math.Abs(res.Coefficients["x2"] - target)
		if gap >= prevGap {
			t.Errorf("λ=%g: |β(x2) − μ₀| = %g did not shrink (previous %g)", lam, gap, prevGap)
		}
		prevGap = gap
		if lam == 1e12 && gap > 1e-9 {
			t.Errorf("λ=1e12: β(x2) = %.17g, want its prior mean %v", res.Coefficients["x2"], target)
		}
	}
}

// TestRegBayes_FlatPriorImproperRefused: a flat (0) entry is fine while
// the data identify the coefficient; on a duplicated predictor with both
// slopes flat the posterior is improper and the fit is the existing
// RANK_DEFICIENT refusal, and one proper entry on the pair restores it.
func TestRegBayes_FlatPriorImproperRefused(t *testing.T) {
	recs := make([]Record, 20)
	for i := range recs {
		x := float64(i%7) - 2
		recs[i] = mockRecord{values: map[string]float64{"x": x, "x_dup": x, "y": 1 + 2*x + 0.1*math.Sin(float64(i))}}
	}
	spec := func(v []float64) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x", "x_dup"},
			PriorPrecision: types.PrecisionVector(v)}
	}
	_, err := newBayes(t, spec([]float64{0, 0, 0})).FitBuffered(recs)
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PROCESSING_REGRESSION_RANK_DEFICIENT {
		t.Fatalf("improper flat prior: err = %v, want PROCESSING_REGRESSION_RANK_DEFICIENT", err)
	}
	if _, err := newBayes(t, spec([]float64{0, 0, 1})).FitBuffered(recs); err != nil {
		t.Fatalf("one proper entry: %v", err)
	}
}
