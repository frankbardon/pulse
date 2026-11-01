package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// mapRow is a model.Row over a name → value map (absent = null).
type mapRow map[string]float64

func (r mapRow) NumericValue(name string) (float64, bool) {
	v, ok := r[name]
	return v, ok
}

// TestInfluenceFigures_Undefined: a figure the fit leaves undefined is
// NaN, never ±Inf — h ≥ 1, a non-positive φ, df ≤ 1 where σ₍ᵢ₎ is read
// — and a fixed dispersion makes the studentized residual the
// standardized one.
func TestInfluenceFigures_Undefined(t *testing.T) {
	t.Parallel()

	all := func(f RowInfluence) []float64 { return []float64{f.Standardized, f.Studentized, f.Cooks, f.DFFITS} }
	for name, f := range map[string]RowInfluence{
		"h = 1":  InfluenceFigures(0.5, 1, 1, 10, 3, true, 1),
		"φ = 0":  InfluenceFigures(0.5, 0.2, 0, 10, 3, true, 1),
		"φ NaN":  InfluenceFigures(0.5, 0.2, math.NaN(), 10, 3, false, 1),
		"h NaN":  InfluenceFigures(0.5, math.NaN(), 1, 10, 3, true, 1),
		"σᵢ ≤ 0": InfluenceFigures(10, 0.2, 1, 2, 3, true, 1),
	} {
		for i, v := range all(f) {
			if name == "σᵢ ≤ 0" && i == 0 || name == "σᵢ ≤ 0" && i == 2 {
				continue // standardized and cooks stay defined
			}
			if !math.IsNaN(v) {
				t.Errorf("%s: figure %d = %v, want NaN", name, i, v)
			}
		}
	}
	if f := InfluenceFigures(0.5, 0.2, 1, 1, 3, true, 1); !math.IsNaN(f.Studentized) || !math.IsNaN(f.DFFITS) || math.IsNaN(f.Standardized) {
		t.Errorf("df = 1: %+v, want studentized / dffits NaN, standardized defined", f)
	}
	f := InfluenceFigures(0.5, 0.2, 1, 10, 3, false, -1)
	if f.Studentized != f.Standardized || !(f.DFFITS < 0) {
		t.Errorf("fixed φ: %+v, want studentized = standardized and a negative-signed dffits", f)
	}
}

// TestBoundInfluence_NullsAndPoisson: a null predictor nulls the row;
// over a poisson fit the leverage is w·x̃ᵀ(Vcov)x̃ with w = μ (log link:
// dμ/dη = μ, V = μ), and a fit without a vcov is an engine invariant.
func TestBoundInfluence_NullsAndPoisson(t *testing.T) {
	t.Parallel()

	res := &types.RegressionResult{Type: types.REG_GLM, Family: "poisson", Link: "log", NObs: 20,
		Coefficients: map[string]float64{model.InterceptKey: 0.5, "x": 0.25},
		Vcov: &types.MatrixValues{RowKeys: []string{model.InterceptKey, "x"}, ColumnKeys: []string{model.InterceptKey, "x"},
			Values: [][]float64{{0.04, -0.01}, {-0.01, 0.02}}}}
	spec := &types.RegressionSpec{Type: types.REG_GLM, Family: "poisson", Target: "y", Predictors: []string{"x"}, Vcov: true}
	b, err := model.Bind("m", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewBoundInfluence(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Leverage(mapRow{}); ok {
		t.Error("a null predictor scores a leverage")
	}
	x := 2.0
	mu := math.Exp(0.5 + 0.25*x)
	want := mu * (0.04 + 2*x*-0.01 + x*x*0.02)
	if h, ok := d.Leverage(mapRow{"x": x}); !ok || math.Abs(h-want) > 1e-15*want {
		t.Errorf("leverage = %v (%v), want %v", h, ok, want)
	}
	inf, ok := d.Row(mapRow{"x": x}, 3)
	if !ok || math.Abs(inf.Standardized-(3-mu)/math.Sqrt(mu)/math.Sqrt(1-want)) > 1e-14 || inf.Studentized != inf.Standardized {
		t.Errorf("row figures %+v", inf)
	}
	b.Vcov = nil
	if _, err := NewBoundInfluence(b); err == nil {
		t.Error("a fit without a vcov resolves a design")
	}
}
