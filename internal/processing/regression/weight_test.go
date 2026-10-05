package regression

import (
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
	"gonum.org/v1/gonum/mat"
)

// Weighted regressions (U12 E4-S1): REG_OLS plain and penalised under
// both weight kinds, REG_BAYES_LINEAR under frequency. Oracles:
//   - unity: w ≡ 1 under either kind is the unweighted fit bit for bit
//     once SumWeights / NEff are shed;
//   - frequency: integer weights equal the physically expanded rows;
//   - probability: the closed-form WLS on w* = w·n_eff/Σw (an explicit
//     design-matrix solve with an intercept column, no Welford), df =
//     n_eff − p − 1;
//   - penalised: β is invariant to rescaling the weights and identical
//     across kinds.

type wRegRow struct {
	x1, x2, y, w float64
}

// wRegFixture: 14 rows, two predictors, uneven integer weights.
func wRegFixture() []wRegRow {
	x1 := []float64{1.2, 2.5, 3.1, 4.8, 5.0, 6.3, 7.7, 8.1, 9.4, 10.2, 11.8, 12.5, 13.1, 14.9}
	x2 := []float64{0.4, -1.3, 2.2, 0.9, -0.5, 1.7, 3.3, -2.1, 0.0, 1.1, -0.8, 2.6, 1.4, -1.9}
	noise := []float64{0.9, -0.4, 1.3, 0.2, -0.8, 0.5, -1.1, 0.7, -0.3, 1.0, -0.6, 0.1, -1.2, 0.4}
	ws := []float64{1, 3, 2, 1, 4, 2, 1, 5, 1, 2, 3, 1, 2, 1}
	out := make([]wRegRow, len(x1))
	for i := range x1 {
		out[i] = wRegRow{x1: x1[i], x2: x2[i], y: 1.5 + 0.8*x1[i] - 1.2*x2[i] + noise[i], w: ws[i]}
	}
	return out
}

func wRegRecords(rows []wRegRow) []Record {
	out := make([]Record, len(rows))
	for i, r := range rows {
		out[i] = mockRecord{values: map[string]float64{"x1": r.x1, "x2": r.x2, "y": r.y, "w": r.w}}
	}
	return out
}

// expand replicates each row w times (frequency weights).
func expand(rows []wRegRow) []wRegRow {
	var out []wRegRow
	for _, r := range rows {
		for k := 0; k < int(r.w); k++ {
			out = append(out, wRegRow{x1: r.x1, x2: r.x2, y: r.y, w: 1})
		}
	}
	return out
}

func scaleWeights(rows []wRegRow, c float64) []wRegRow {
	out := append([]wRegRow(nil), rows...)
	for i := range out {
		out[i].w *= c
	}
	return out
}

type wRegRows []wRegRow

func (rows wRegRows) withW(w float64) []wRegRow {
	out := append([]wRegRow(nil), rows...)
	for i := range out {
		out[i].w = w
	}
	return out
}

// regSpecs are the weight-aware regression variants, unweighted.
func regSpecs() map[string]types.RegressionSpec {
	base := types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"}}
	ridge, lasso, enet := base, base, base
	ridge.Penalty, ridge.Alpha = "l2", 0.3
	lasso.Penalty, lasso.Alpha, lasso.Tol = "l1", 0.05, 1e-12
	enet.Penalty, enet.Alpha, enet.L1Ratio, enet.Tol = "elasticnet", 0.05, 0.5, 1e-12
	bayes := types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x1", "x2"}}
	return map[string]types.RegressionSpec{"ols": base, "ridge": ridge, "lasso": lasso, "elasticnet": enet, "bayes": bayes}
}

// fitWeighted fits spec over rows; kind "" runs unweighted (the weight
// column is still present on every row).
func fitWeighted(t *testing.T, spec types.RegressionSpec, rows []wRegRow, kind types.WeightKind) *types.RegressionResult {
	t.Helper()
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	var be BufferedEngine
	if spec.Type == types.REG_BAYES_LINEAR {
		be = newBayes(t, &spec)
	} else {
		be = newOLS(t, &spec)
	}
	res, err := be.FitBuffered(wRegRecords(rows))
	if err != nil {
		t.Fatalf("%s fit: %v", spec.Type, err)
	}
	return res
}

func kindsFor(spec types.RegressionSpec) []types.WeightKind {
	if spec.Type == types.REG_BAYES_LINEAR {
		return []types.WeightKind{types.WeightKindFrequency}
	}
	return []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}
}

func relClose(a, b, tol float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= tol*math.Max(math.Abs(a), math.Abs(b))
}

func mapsClose(t *testing.T, what string, got, want map[string]float64, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: keys %v, want %v", what, got, want)
	}
	for k, w := range want {
		if !relClose(got[k], w, tol) {
			t.Errorf("%s[%s] = %.17g, want %.17g", what, k, got[k], w)
		}
	}
}

// TestWeightedRegression_UnityBitIdentical: all-ones weights under
// either kind reproduce the unweighted fit bit for bit, with
// SumWeights = n and (probability) NEff = n.
func TestWeightedRegression_UnityBitIdentical(t *testing.T) {
	rows := wRegRows(wRegFixture()).withW(1)
	for name, spec := range regSpecs() {
		base := fitWeighted(t, spec, rows, "")
		if base.SumWeights != 0 || base.NEff != 0 {
			t.Fatalf("%s: unweighted fit carries weight keys %v / %v", name, base.SumWeights, base.NEff)
		}
		for _, kind := range kindsFor(spec) {
			got := fitWeighted(t, spec, rows, kind)
			n := float64(len(rows))
			if got.SumWeights != n {
				t.Fatalf("%s/%s: sum_weights %v, want %v", name, kind, got.SumWeights, n)
			}
			if (kind == types.WeightKindProbability) != (got.NEff != 0) || (got.NEff != 0 && got.NEff != n) {
				t.Fatalf("%s/%s: n_eff %v", name, kind, got.NEff)
			}
			got.SumWeights, got.NEff = 0, 0
			if !reflect.DeepEqual(got, base) {
				t.Fatalf("%s/%s: unity differs from unweighted:\n%+v\n%+v", name, kind, got, base)
			}
		}
	}
}

// TestWeightedRegression_FrequencyIsExpansion: integer frequency
// weights equal the expanded rows run unweighted (every variant), with
// NObs the raw row count and SumWeights the expanded n.
func TestWeightedRegression_FrequencyIsExpansion(t *testing.T) {
	rows := wRegFixture()
	exp := expand(rows)
	for name, spec := range regSpecs() {
		got := fitWeighted(t, spec, rows, types.WeightKindFrequency)
		want := fitWeighted(t, spec, exp, "")
		if got.NObs != len(rows) || got.SumWeights != float64(len(exp)) || got.NEff != 0 {
			t.Fatalf("%s: n_obs %d sum_weights %v n_eff %v (rows %d, expanded %d)", name, got.NObs, got.SumWeights, got.NEff, len(rows), len(exp))
		}
		tol := 1e-10
		if spec.Penalty == "l1" || spec.Penalty == "elasticnet" {
			tol = 1e-8 // coordinate descent converges to its tolerance
		}
		mapsClose(t, name+" coefficients", got.Coefficients, want.Coefficients, tol)
		mapsClose(t, name+" std_errors", got.StdErrors, want.StdErrors, tol)
		mapsClose(t, name+" p_values", got.PValues, want.PValues, 1e-8)
		for _, f := range [][3]any{{"r2", got.R2, want.R2}, {"adj_r2", got.AdjR2, want.AdjR2}, {"residual_std_err", got.ResidualStdErr, want.ResidualStdErr}} {
			if !relClose(f[1].(float64), f[2].(float64), tol) {
				t.Errorf("%s %s = %.17g, want %.17g", name, f[0], f[1], f[2])
			}
		}
		if spec.Type == types.REG_BAYES_LINEAR {
			for k, ci := range want.CredibleIntervals {
				if !relClose(got.CredibleIntervals[k][0], ci[0], tol) || !relClose(got.CredibleIntervals[k][1], ci[1], tol) {
					t.Errorf("%s credible[%s] = %v, want %v", name, k, got.CredibleIntervals[k], ci)
				}
			}
		}
	}
}

// wlsReference is the closed-form weighted fit on w* = w·N*/Σw: an
// explicit design matrix with an intercept column, β = (Z'W*Z)⁻¹Z'W*y,
// σ̂² = Σw*·e²/(N* − q), Var(β) = σ̂²(Z'W*Z)⁻¹, t p-values on N* − q.
type wlsRef struct {
	coef, se, p                  map[string]float64
	r2, adjR2, residualSE, nStar float64
}

func wlsReference(t *testing.T, rows []wRegRow, prob bool) wlsRef {
	t.Helper()
	var sw, sw2 float64
	for _, r := range rows {
		sw += r.w
		sw2 += r.w * r.w
	}
	nStar := sw
	if prob {
		nStar = sw * sw / sw2
	}
	c := nStar / sw
	const q = 3
	a := mat.NewSymDense(q, nil)
	b := mat.NewVecDense(q, nil)
	for _, r := range rows {
		z := []float64{1, r.x1, r.x2}
		ws := r.w * c
		for i := 0; i < q; i++ {
			for j := i; j < q; j++ {
				a.SetSym(i, j, a.At(i, j)+ws*z[i]*z[j])
			}
			b.SetVec(i, b.AtVec(i)+ws*z[i]*r.y)
		}
	}
	var chol mat.Cholesky
	if !chol.Factorize(a) {
		t.Fatal("reference Gram not PD")
	}
	var beta mat.VecDense
	if err := chol.SolveVecTo(&beta, b); err != nil {
		t.Fatal(err)
	}
	var inv mat.SymDense
	if err := chol.InverseTo(&inv); err != nil {
		t.Fatal(err)
	}
	var ybar float64
	for _, r := range rows {
		ybar += r.w * r.y
	}
	ybar /= sw
	var rss, tss float64
	for _, r := range rows {
		e := r.y - (beta.AtVec(0) + beta.AtVec(1)*r.x1 + beta.AtVec(2)*r.x2)
		rss += r.w * c * e * e
		tss += r.w * c * (r.y - ybar) * (r.y - ybar)
	}
	df := nStar - q
	sigma2 := rss / df
	names := []string{InterceptKey, "x1", "x2"}
	ref := wlsRef{coef: map[string]float64{}, se: map[string]float64{}, p: map[string]float64{}, nStar: nStar}
	for i, n := range names {
		ref.coef[n] = beta.AtVec(i)
		ref.se[n] = math.Sqrt(sigma2 * inv.At(i, i))
		ref.p[n] = statdist.StudentTTwoSidedP(ref.coef[n]/ref.se[n], df)
	}
	ref.r2 = 1 - rss/tss
	ref.adjR2 = 1 - (1-ref.r2)*(nStar-1)/df
	ref.residualSE = math.Sqrt(sigma2)
	return ref
}

// TestWeightedRegression_OLSClosedForm: plain weighted OLS matches the
// explicit WLS-on-w* reference under both kinds (Kish: df = n_eff −
// p − 1, fractional); probability SEs are wider than frequency ones on
// the same weights.
func TestWeightedRegression_OLSClosedForm(t *testing.T) {
	rows := wRegFixture()
	spec := regSpecs()["ols"]
	for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
		prob := kind == types.WeightKindProbability
		got := fitWeighted(t, spec, rows, kind)
		ref := wlsReference(t, rows, prob)
		mapsClose(t, string(kind)+" coefficients", got.Coefficients, ref.coef, 1e-10)
		mapsClose(t, string(kind)+" std_errors", got.StdErrors, ref.se, 1e-10)
		mapsClose(t, string(kind)+" p_values", got.PValues, ref.p, 1e-8)
		for _, f := range [][3]any{{"r2", got.R2, ref.r2}, {"adj_r2", got.AdjR2, ref.adjR2}, {"residual_std_err", got.ResidualStdErr, ref.residualSE}} {
			if !relClose(f[1].(float64), f[2].(float64), 1e-10) {
				t.Errorf("%s %s = %.17g, want %.17g", kind, f[0], f[1], f[2])
			}
		}
		if prob && !relClose(got.NEff, ref.nStar, 1e-14) {
			t.Errorf("n_eff %v, want %v", got.NEff, ref.nStar)
		}
	}
	freq := fitWeighted(t, spec, rows, types.WeightKindFrequency)
	prob := fitWeighted(t, spec, rows, types.WeightKindProbability)
	if !(prob.StdErrors["x1"] > freq.StdErrors["x1"]) {
		t.Fatalf("Kish SE %v not wider than frequency SE %v", prob.StdErrors["x1"], freq.StdErrors["x1"])
	}
}

// TestWeightedRegression_ProbabilityScaleInvariant: probability weights
// are scale-free — weights × 0.37 answer the same fit (coefficients,
// SEs, p-values, R²s, residual SE and n_eff), SumWeights × 0.37.
func TestWeightedRegression_ProbabilityScaleInvariant(t *testing.T) {
	rows := wRegFixture()
	for name, spec := range regSpecs() {
		if spec.Type == types.REG_BAYES_LINEAR {
			continue
		}
		a := fitWeighted(t, spec, rows, types.WeightKindProbability)
		b := fitWeighted(t, spec, scaleWeights(rows, 0.37), types.WeightKindProbability)
		tol := 1e-10
		if spec.Penalty == "l1" || spec.Penalty == "elasticnet" {
			tol = 1e-8
		}
		mapsClose(t, name+" coefficients", b.Coefficients, a.Coefficients, tol)
		mapsClose(t, name+" std_errors", b.StdErrors, a.StdErrors, tol)
		mapsClose(t, name+" p_values", b.PValues, a.PValues, 1e-8)
		if !relClose(b.ResidualStdErr, a.ResidualStdErr, tol) || !relClose(b.AdjR2, a.AdjR2, tol) ||
			!relClose(b.NEff, a.NEff, 1e-12) || !relClose(b.SumWeights, 0.37*a.SumWeights, 1e-12) {
			t.Errorf("%s: scaled fit %+v vs %+v", name, b, a)
		}
	}
}

// TestWeightedRegression_PenalisedBetaKindFree: a penalised fit's β is
// identical under frequency and probability on the same weights (the
// penalty is scaled by Σw, never N*), and only its SEs move.
func TestWeightedRegression_PenalisedBetaKindFree(t *testing.T) {
	rows := wRegFixture()
	for _, name := range []string{"ols", "ridge", "lasso", "elasticnet"} {
		spec := regSpecs()[name]
		f := fitWeighted(t, spec, rows, types.WeightKindFrequency)
		p := fitWeighted(t, spec, rows, types.WeightKindProbability)
		if !reflect.DeepEqual(f.Coefficients, p.Coefficients) || f.R2 != p.R2 {
			t.Fatalf("%s: β differs across kinds: %v vs %v", name, f.Coefficients, p.Coefficients)
		}
		if f.StdErrors["x1"] == p.StdErrors["x1"] {
			t.Fatalf("%s: SEs did not read N*", name)
		}
	}
}

// TestWeightedRegression_ExcludesInvalidAndZero: a row whose weight is
// null, negative, NaN, zero or (frequency) fractional never enters the
// fit or NObs; a row dropped listwise for a null predictor is dropped
// before its weight is judged.
func TestWeightedRegression_ExcludesInvalidAndZero(t *testing.T) {
	rows := wRegFixture()
	recs := wRegRecords(rows)
	bad := []Record{
		mockRecord{values: map[string]float64{"x1": 99, "x2": 9, "y": -50}, nulls: map[string]bool{"w": true}},
		mockRecord{values: map[string]float64{"x1": 98, "x2": 8, "y": 70, "w": -2}},
		mockRecord{values: map[string]float64{"x1": 97, "x2": 7, "y": 40, "w": math.NaN()}},
		mockRecord{values: map[string]float64{"x1": 96, "x2": 6, "y": -30, "w": 0}},
		mockRecord{values: map[string]float64{"x1": 95, "x2": 5, "y": 20, "w": 2.5}},
		mockRecord{values: map[string]float64{"x2": 4, "y": 10, "w": 3}, nulls: map[string]bool{"x1": true}},
	}
	for name, spec := range regSpecs() {
		for _, kind := range kindsFor(spec) {
			s := spec
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
			var be BufferedEngine
			if s.Type == types.REG_BAYES_LINEAR {
				be = newBayes(t, &s)
			} else {
				be = newOLS(t, &s)
			}
			in := recs
			if kind == types.WeightKindFrequency {
				in = append(append([]Record(nil), recs...), bad...)
			} else {
				// 2.5 is a valid probability weight: leave it out here.
				in = append(append(append([]Record(nil), recs...), bad[:4]...), bad[5])
			}
			got, err := be.FitBuffered(in)
			if err != nil {
				t.Fatal(err)
			}
			want := fitWeighted(t, spec, rows, kind)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s/%s: invalid rows leaked:\n%+v\n%+v", name, kind, got, want)
			}
		}
	}
}

// TestWeightedRegression_StreamingMatchesBuffered: the streaming
// UpdateRow / Finalize path and FitBuffered answer the same weighted
// fit bit for bit.
func TestWeightedRegression_StreamingMatchesBuffered(t *testing.T) {
	rows := wRegFixture()
	for name, spec := range regSpecs() {
		for _, kind := range kindsFor(spec) {
			s := spec
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
			if !s.Streamable() {
				t.Fatalf("%s: weighted spec not streamable", name)
			}
			var eng Engine
			var err error
			if s.Type == types.REG_BAYES_LINEAR {
				eng, err = newBayesLinearEngine(&s, schemaWithFields("y", "x1", "x2", "w"))
			} else {
				eng, err = newOLSEngine(&s, schemaWithFields("y", "x1", "x2", "w"))
			}
			if err != nil {
				t.Fatal(err)
			}
			se := eng.(StreamingEngine)
			for _, r := range wRegRecords(rows) {
				if err := se.UpdateRow(r); err != nil {
					t.Fatal(err)
				}
			}
			got, err := se.Finalize()
			if err != nil {
				t.Fatal(err)
			}
			if want := fitWeighted(t, spec, rows, kind); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s/%s: streaming differs:\n%+v\n%+v", name, kind, got, want)
			}
		}
	}
}
