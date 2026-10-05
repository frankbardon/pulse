package regression

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Weighted REG_GLM (U12 E4-S2): IRLS with working weights w*·μ'(η)²/V(μ),
// covariance (XᵀW*X)⁻¹ and deviances on w* = w·N*/Σw. Oracles here:
//   - unity: w ≡ 1 under either kind is the unweighted fit bit for bit;
//   - frequency: integer weights equal the physically expanded rows.
//
// The external pins — stock R glm on the expanded rows (frequency), IRLS
// on w* cross-checked with statsmodels (probability), dispersion fixed
// at 1 — are generated with every other weighted regression's into
// internal/service/weight_reference_values_test.go by
// testdata/weight_reference/gen_weight_reference.py
// (TestWeightReferenceValues/regressions).

type wGLMRow struct{ x1, x2, yb, yp, yg, w float64 }

// wGLMFixture: 20 rows, two predictors, a binomial / poisson / gamma
// target each, uneven integer weights.
func wGLMFixture() []wGLMRow {
	x1 := []float64{-2.1, -1.7, -1.2, -0.9, -0.5, -0.3, 0.0, 0.2, 0.4, 0.7, 0.9, 1.1, 1.4, 1.6, 1.9, 2.2, 2.5, -0.7, 0.6, 1.3}
	x2 := []float64{0.5, -0.3, 1.1, 0.2, -0.8, 0.9, -0.4, 1.3, 0.1, -1.0, 0.6, -0.2, 0.8, -0.6, 0.3, -1.2, 0.7, 0.4, -0.5, 1.0}
	yb := []float64{0, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 0, 1, 1, 1, 0, 1, 0, 0, 1}
	yp := []float64{0, 1, 2, 0, 1, 3, 1, 4, 2, 1, 3, 2, 5, 3, 4, 2, 6, 1, 2, 5}
	yg := []float64{0.8, 1.1, 2.3, 0.9, 1.0, 2.9, 1.2, 3.8, 1.9, 1.1, 2.7, 1.6, 4.2, 2.5, 3.6, 1.9, 5.1, 1.4, 1.7, 4.4}
	w := []float64{1, 3, 2, 1, 4, 2, 1, 5, 1, 2, 3, 1, 2, 1, 2, 3, 1, 2, 4, 1}
	out := make([]wGLMRow, len(x1))
	for i := range x1 {
		out[i] = wGLMRow{x1[i], x2[i], yb[i], yp[i], yg[i], w[i]}
	}
	return out
}

func wGLMRecords(rows []wGLMRow) []Record {
	out := make([]Record, len(rows))
	for i, r := range rows {
		out[i] = mockRecord{values: map[string]float64{"x1": r.x1, "x2": r.x2, "yb": r.yb, "yp": r.yp, "yg": r.yg, "w": r.w}}
	}
	return out
}

var wGLMTargets = map[string]string{"binomial": "yb", "poisson": "yp", "gamma": "yg"}

func fitWeightedGLM(t *testing.T, family string, rows []wGLMRow, kind types.WeightKind) *types.RegressionResult {
	t.Helper()
	spec := &types.RegressionSpec{Type: types.REG_GLM, Family: family, Target: wGLMTargets[family], Predictors: []string{"x1", "x2"}, Tol: 1e-14, MaxIters: 100}
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(wGLMRecords(rows))
	if err != nil {
		t.Fatalf("%s/%s fit: %v", family, kind, err)
	}
	return res
}

// TestWeightedGLM_UnityBitIdentical: all-ones weights under either kind
// reproduce the unweighted fit bit for bit once SumWeights / NEff are
// shed.
func TestWeightedGLM_UnityBitIdentical(t *testing.T) {
	rows := wGLMFixture()
	for i := range rows {
		rows[i].w = 1
	}
	for family := range wGLMTargets {
		base := fitWeightedGLM(t, family, rows, "")
		if base.SumWeights != 0 || base.NEff != 0 {
			t.Fatalf("%s: unweighted fit carries weight keys", family)
		}
		for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
			got := fitWeightedGLM(t, family, rows, kind)
			if got.SumWeights != float64(len(rows)) {
				t.Fatalf("%s/%s: sum_weights %v", family, kind, got.SumWeights)
			}
			got.SumWeights, got.NEff = 0, 0
			if !reflect.DeepEqual(got, base) {
				t.Fatalf("%s/%s: unity differs from unweighted:\n%+v\n%+v", family, kind, got, base)
			}
		}
	}
}

// TestWeightedGLM_FrequencyIsExpansion: integer frequency weights equal
// the expanded rows run unweighted, NObs staying the raw row count.
func TestWeightedGLM_FrequencyIsExpansion(t *testing.T) {
	rows := wGLMFixture()
	var exp []wGLMRow
	for _, r := range rows {
		for k := 0; k < int(r.w); k++ {
			e := r
			e.w = 1
			exp = append(exp, e)
		}
	}
	for family := range wGLMTargets {
		got := fitWeightedGLM(t, family, rows, types.WeightKindFrequency)
		want := fitWeightedGLM(t, family, exp, "")
		if got.NObs != len(rows) || got.SumWeights != float64(len(exp)) {
			t.Fatalf("%s: n_obs %d sum_weights %v", family, got.NObs, got.SumWeights)
		}
		mapsClose(t, family+" coefficients", got.Coefficients, want.Coefficients, 1e-9)
		mapsClose(t, family+" std_errors", got.StdErrors, want.StdErrors, 1e-9)
		mapsClose(t, family+" p_values", got.PValues, want.PValues, 1e-7)
		for _, f := range [][3]float64{{got.Deviance, want.Deviance}, {got.NullDeviance, want.NullDeviance}, {got.PseudoR2, want.PseudoR2}} {
			if !relClose(f[0], f[1], 1e-9) {
				t.Errorf("%s deviance-family figure %.17g, want %.17g", family, f[0], f[1])
			}
		}
	}
}

// TestWeightedGLM_ExcludesInvalidAndZero: a zero, negative or null
// weight drops its row — the fit equals the fixture without those rows
// and NObs never counts them.
func TestWeightedGLM_ExcludesInvalidAndZero(t *testing.T) {
	rows := wGLMFixture()
	recs := wGLMRecords(rows)
	recs[0] = mockRecord{values: map[string]float64{"x1": rows[0].x1, "x2": rows[0].x2, "yb": rows[0].yb, "yp": rows[0].yp, "yg": rows[0].yg, "w": 0}}
	recs[1] = mockRecord{values: map[string]float64{"x1": rows[1].x1, "x2": rows[1].x2, "yb": rows[1].yb, "yp": rows[1].yp, "yg": rows[1].yg, "w": -2}}
	recs[2] = mockRecord{values: map[string]float64{"x1": rows[2].x1, "x2": rows[2].x2, "yb": rows[2].yb, "yp": rows[2].yp, "yg": rows[2].yg}}
	spec := &types.RegressionSpec{Type: types.REG_GLM, Family: "poisson", Target: "yp", Predictors: []string{"x1", "x2"},
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})}
	got, err := asBuffered(t, newGLM(t, spec)).FitBuffered(recs)
	if err != nil {
		t.Fatal(err)
	}
	spec2 := *spec
	want, err := asBuffered(t, newGLM(t, &spec2)).FitBuffered(wGLMRecords(rows[3:]))
	if err != nil {
		t.Fatal(err)
	}
	if got.NObs != len(rows)-3 {
		t.Fatalf("n_obs %d, want %d", got.NObs, len(rows)-3)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("excluded rows leak into the fit:\n%+v\n%+v", got, want)
	}
}
