package regression

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Weighted REG_GLM (U12 E4-S2): IRLS with working weights w*·μ'(η)²/V(μ),
// covariance (XᵀW*X)⁻¹ and deviances on w* = w·N*/Σw. Oracles:
//   - R glm(weights = w) under kind frequency and glm(weights = w*)
//     under kind probability (summary(dispersion = 1): the engine fixes
//     the dispersion at 1, the gamma caveat included);
//   - unity: w ≡ 1 under either kind is the unweighted fit bit for bit;
//   - frequency: integer weights equal the physically expanded rows.

type wGLMRow struct{ x1, x2, yb, yp, yg, w float64 }

// wGLMFixture is the 20-row fixture of the R reference below.
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

// glmWeightReference: R 4.6.1, glm(y ~ x1 + x2, family, weights,
// control = glm.control(epsilon = 1e-14, maxit = 100)),
// summary(fit, dispersion = 1); weights = w (frequency) or
// w·n_eff/Σw (probability). Gamma uses its default inverse link.
var glmWeightReference = []struct {
	family, kind           string
	coef, se               [3]float64
	deviance, nullDeviance float64
}{
	{family: "binomial", kind: "frequency", coef: [3]float64{-3.0123671154525913, 6.2072601453509284, 12.073352794970646}, se: [3]float64{2.0408897550834153, 3.5981787479360063, 6.2728498369037595}, deviance: 6.5209742458225168, nullDeviance: 58.129089045877386},
	{family: "binomial", kind: "probability", coef: [3]float64{-3.0123671154525868, 6.2072601453509213, 12.07335279497064}, se: [3]float64{3.3917504736493487, 5.9798058396689404, 10.42483620655306}, deviance: 2.361042399349532, nullDeviance: 21.046739137300431},
	{family: "poisson", kind: "frequency", coef: [3]float64{0.50796211695981919, 0.40601848551848069, 0.56333931386166392}, se: [3]float64{0.13929371203167645, 0.09499671405715121, 0.13292216785331715}, deviance: 6.8275023050037715, nullDeviance: 40.220172054469074},
	{family: "poisson", kind: "probability", coef: [3]float64{0.50796211695981941, 0.40601848551848058, 0.5633393138616638}, se: [3]float64{0.23149193303549268, 0.15787484336772484, 0.2209030768929123}, deviance: 2.4720266966392979, nullDeviance: 14.562476088687081},
	{family: "gamma", kind: "frequency", coef: [3]float64{0.611632574579001, -0.12332341412097045, -0.23849724226017716}, se: [3]float64{0.10632390146244111, 0.05462734416071717, 0.096231561920791756}, deviance: 1.265643874982624, nullDeviance: 11.558254910041763},
	{family: "gamma", kind: "probability", coef: [3]float64{0.61163257457900067, -0.12332341412097032, -0.23849724226017702}, se: [3]float64{0.17669947277891851, 0.090785070710756852, 0.1599270344881244}, deviance: 0.45825036852819151, nullDeviance: 4.1848853984633969},
}

// TestWeightedGLM_MatchesR: every family under both kinds matches R's
// weighted glm — coefficients, dispersion-1 standard errors (hence the
// Wald-z p-values), deviance and null deviance — with NObs the raw row
// count, sum_weights Σw and (probability) n_eff Kish.
func TestWeightedGLM_MatchesR(t *testing.T) {
	rows := wGLMFixture()
	sumW, sumWSq := 0.0, 0.0
	for _, r := range rows {
		sumW += r.w
		sumWSq += r.w * r.w
	}
	names := []string{InterceptKey, "x1", "x2"}
	// The binomial fixture sits near separation (|β| ≈ 12), where two
	// IRLS implementations agree to ~1e-9 only; 1e-7 still pins every
	// figure far below any weighting mistake (w vs w* moves SEs ~1.7×).
	const tol = 1e-7
	for _, ref := range glmWeightReference {
		got := fitWeightedGLM(t, ref.family, rows, types.WeightKind(ref.kind))
		what := ref.family + "/" + ref.kind
		for i, k := range names {
			if !relClose(got.Coefficients[k], ref.coef[i], tol) {
				t.Errorf("%s coef[%s] = %.17g, want %.17g", what, k, got.Coefficients[k], ref.coef[i])
			}
			if !relClose(got.StdErrors[k], ref.se[i], tol) {
				t.Errorf("%s se[%s] = %.17g, want %.17g", what, k, got.StdErrors[k], ref.se[i])
			}
			wantP := math.Erfc(math.Abs(ref.coef[i]/ref.se[i]) / math.Sqrt2)
			if !relClose(got.PValues[k], wantP, tol) {
				t.Errorf("%s p[%s] = %.17g, want %.17g", what, k, got.PValues[k], wantP)
			}
		}
		if !relClose(got.Deviance, ref.deviance, 1e-9) || !relClose(got.NullDeviance, ref.nullDeviance, 1e-9) {
			t.Errorf("%s deviance %.17g / null %.17g, want %.17g / %.17g", what, got.Deviance, got.NullDeviance, ref.deviance, ref.nullDeviance)
		}
		if got.NObs != len(rows) || got.SumWeights != sumW {
			t.Errorf("%s n_obs %d sum_weights %v, want %d / %v", what, got.NObs, got.SumWeights, len(rows), sumW)
		}
		wantNEff := 0.0
		if ref.kind == "probability" {
			wantNEff = sumW * sumW / sumWSq
		}
		if got.NEff != wantNEff {
			t.Errorf("%s n_eff %v, want %v", what, got.NEff, wantNEff)
		}
	}
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
