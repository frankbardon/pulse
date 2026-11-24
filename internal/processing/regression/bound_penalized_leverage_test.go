package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// bound_penalized_leverage_test.go pins a penalized BOUND fit's
// leverage (regression-depth-2 E1-S4, #677): a penalized REG_OLS /
// REG_GLM fit carries its regularized inverse (XᵀWX + λ₂·P)⁻¹ over its
// active design (RegressionResult.PenalizedInverse) and the bound-fit
// design kernel reads it.

// boundLeverage binds res (fitted from spec) and returns its design.
func boundLeverage(t *testing.T, spec *types.RegressionSpec, res *types.RegressionResult) *BoundInfluence {
	t.Helper()
	if res.PenalizedInverse == nil {
		t.Fatal("penalized fit carries no penalized_inverse")
	}
	b, err := model.Bind("m", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewBoundInfluence(b)
	if err != nil {
		t.Fatalf("NewBoundInfluence: %v", err)
	}
	return d
}

func rowOf(names []string, x []float64) mapRow {
	r := mapRow{}
	for j, n := range names {
		r[n] = x[j]
	}
	return r
}

// TestBoundPenalizedLeverage_MatchesRefit: on every E1-S2 oracle case —
// ridge, lasso, elastic net, frequency-weighted, unpenalized predictors
// — a bound penalized REG_OLS fit's leverage equals the penalized
// refit's hat (the one hat design, leverageDesign) for one copy of the
// row to 1e-12 relative, and so R's closed form on the uncentred design
// (gen_ols_penalty.R penalized_hat, divided by the row's frequency
// weight). The carried keys are the intercept and the hat's active
// columns.
func TestBoundPenalizedLeverage_MatchesRefit(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, c := range loadOLSPenaltyReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			seen[c.Penalty] = true
			if c.Weighted {
				seen["weighted"] = true
			}
			if len(c.Unpenalized) > 0 {
				seen["unpenalized"] = true
			}
			recs := c.records()
			spec := c.spec()
			res, err := newOLS(t, spec).FitBuffered(recs)
			if err != nil {
				t.Fatal(err)
			}
			d := boundLeverage(t, spec, res)
			fit, err := FitForAttribute(c.spec(), recs, true)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := len(res.PenalizedInverse.RowKeys), 1+len(fit.design.Cols); got != want {
				t.Errorf("penalized_inverse carries %d keys, want 1 + |active| = %d", got, want)
			}
			p := len(c.Predictors)
			for i, row := range c.Rows {
				w, ok := fit.RowWeight(recs[i])
				if !ok {
					t.Fatalf("row %d excluded", i)
				}
				hb, ok := d.Leverage(rowOf(c.Predictors, row[:p]))
				if !ok {
					t.Fatalf("row %d: no bound leverage", i)
				}
				if hr := LeverageForRow(fit, row[:p], w) / w; !oracleClose(hb, hr, 1e-12) {
					t.Errorf("row %d: bound h = %.17g, refit h/w = %.17g", i, hb, hr)
				}
				if !oracleClose(hb, c.Leverage[i]/w, 1e-10) {
					t.Errorf("row %d: bound h = %.17g, oracle %.17g", i, hb, c.Leverage[i]/w)
				}
			}
		})
	}
	for _, want := range []string{"l1", "l2", "elasticnet", "weighted", "unpenalized"} {
		if !seen[want] {
			t.Errorf("oracle lacks a %s case", want)
		}
	}
}

// naiveInverse inverts the n × n matrix a by Gauss–Jordan elimination
// with partial pivoting — an implementation independent of the engine's
// equilibrated Cholesky.
func naiveInverse(a [][]float64) [][]float64 {
	n := len(a)
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, 2*n)
		copy(m[i], a[i])
		m[i][n+i] = 1
	}
	for c := 0; c < n; c++ {
		piv := c
		for r := c + 1; r < n; r++ {
			if math.Abs(m[r][c]) > math.Abs(m[piv][c]) {
				piv = r
			}
		}
		m[c], m[piv] = m[piv], m[c]
		inv := 1 / m[c][c]
		for k := range m[c] {
			m[c][k] *= inv
		}
		for r := 0; r < n; r++ {
			if r == c || m[r][c] == 0 {
				continue
			}
			f := m[r][c]
			for k := range m[r] {
				m[r][k] -= f * m[c][k]
			}
		}
	}
	out := make([][]float64, n)
	for i := range out {
		out[i] = m[i][n:]
	}
	return out
}

// TestBoundPenalizedLeverage_GLMClosedForm: a bound penalized REG_GLM
// fit's leverage is hᵢ = wᵢ·x̃ᵢᵀ(X_SᵀW*X_S + λ₂·D_S)⁻¹x̃ᵢ at the final
// IRLS working weights wᵢ = (dμ/dη)²/V(μ) of its own coefficients (W* =
// prior × w), S the intercept and the active predictors (every one under
// l2, the nonzero ones under l1 / elasticnet, λ₂ = N*·α·(1 − l1_ratio)),
// D the penalty mask — recomputed here from the coefficients alone with
// an independent inversion, to 1e-9 relative on every glm_penalty.json
// case (the separated binomial design included). A GLM refit of the
// same spec (ATTR_REG_LEVERAGE params.family + penalty) reads the very
// same design: bit-equal to the bound fit times the row's prior weight.
func TestBoundPenalizedLeverage_GLMClosedForm(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, c := range loadGLMPenaltyReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			recs := c.records()
			spec := c.spec()
			eng, err := newGLMEngine(spec, nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := eng.(*glmEngine).FitBuffered(recs)
			if err != nil {
				t.Fatal(err)
			}
			seen[c.Penalty] = true
			d := boundLeverage(t, spec, res)
			fam, err := familyAtTheta(c.Family, "", 0)
			if err != nil {
				t.Fatal(err)
			}
			p := len(c.Predictors)
			l1 := map[string]float64{"l1": 1, "l2": 0, "elasticnet": c.L1Ratio}[c.Penalty]
			active := []int{0}
			for j, name := range c.Predictors {
				if c.Penalty == "l2" || res.Coefficients[name] != 0 {
					active = append(active, j+1)
				}
			}
			pen := map[string]bool{}
			for _, n := range c.Predictors {
				pen[n] = true
			}
			for _, n := range c.Unpenalized {
				pen[n] = false
			}
			design := func(row []float64) []float64 { return append([]float64{1}, row[:p]...) }
			work := func(row []float64) float64 {
				eta := res.Coefficients[model.InterceptKey]
				for j, n := range c.Predictors {
					eta += res.Coefficients[n] * row[j]
				}
				mu, g := fam.InverseLink(eta), fam.DmuDeta(eta)
				return g * g / fam.Variance(mu)
			}
			q := len(active)
			a := make([][]float64, q)
			for u := range a {
				a[u] = make([]float64, q)
			}
			nStar := 0.0
			for _, row := range c.Rows {
				prior := 1.0
				if c.Weighted {
					prior = row[p+1]
				}
				nStar += prior
				x, w := design(row), prior*work(row)
				for u, ju := range active {
					for v, jv := range active {
						a[u][v] += w * x[ju] * x[jv]
					}
				}
			}
			lambda2 := nStar * c.Alpha * (1 - l1)
			for u, ju := range active {
				if ju > 0 && pen[c.Predictors[ju-1]] {
					a[u][u] += lambda2
				}
			}
			inv := naiveInverse(a)
			fit, err := FitForAttribute(spec, recs, true)
			if err != nil {
				t.Fatalf("GLM refit: %v", err)
			}
			for i, row := range c.Rows {
				x := design(row)
				quad := 0.0
				for u, ju := range active {
					for v, jv := range active {
						quad += x[ju] * inv[u][v] * x[jv]
					}
				}
				want := work(row) * quad
				hb, ok := d.Leverage(rowOf(c.Predictors, row[:p]))
				if !ok {
					t.Fatalf("row %d: no bound leverage", i)
				}
				if !oracleClose(hb, want, 1e-9) {
					t.Errorf("row %d: bound h = %.17g, closed form %.17g", i, hb, want)
				}
				w, ok := fit.RowWeight(recs[i])
				if !ok {
					t.Fatalf("row %d excluded from the refit", i)
				}
				if hr := LeverageForRow(fit, row[:p], w); hr != float64(float64(w*fit.glmScale)*hb) {
					t.Errorf("row %d: refit h = %.17g ≠ w·bound h = %.17g", i, hr, w*hb)
				}
			}
		})
	}
	for _, want := range []string{"l1", "l2", "elasticnet"} {
		if !seen[want] {
			t.Errorf("oracle lacks a %s case", want)
		}
	}
}

// TestBoundPenalizedLeverage_UnpenalizedCarriesNothing: an unpenalized,
// a resampled and a selected fit carry no penalized_inverse (their wire
// form is unchanged); a penalized fit's design ignores its vcov (a ridge
// sandwich), and one without its carriage is an invariant violation.
func TestBoundPenalizedLeverage_UnpenalizedCarriesNothing(t *testing.T) {
	t.Parallel()

	c := loadOLSPenaltyReference(t)[0]
	recs := c.records()
	for _, edit := range []func(*types.RegressionSpec){
		func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio, s.Unpenalized = "", 0, 0, nil },
		func(s *types.RegressionSpec) { s.Resample = "jackknife" },
		func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" },
	} {
		spec := c.spec()
		edit(spec)
		engs, err := Build([]*types.RegressionSpec{spec}, schemaWithFields(append([]string{spec.Target, "w"}, spec.Predictors...)...))
		if err != nil {
			t.Fatal(err)
		}
		res, err := engs[0].(BufferedEngine).FitBuffered(recs)
		if err != nil {
			t.Fatal(err)
		}
		if res.PenalizedInverse != nil {
			t.Errorf("spec %+v carries penalized_inverse", spec)
		}
	}
	spec := c.spec()
	spec.Penalty, spec.Alpha, spec.L1Ratio, spec.Vcov = "l2", 0.3, 0, true
	res, err := newOLS(t, spec).FitBuffered(recs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Vcov == nil {
		t.Fatal("ridge with vcov carries no vcov")
	}
	d := boundLeverage(t, spec, res)
	fit, err := FitForAttribute(spec, recs, true)
	if err != nil {
		t.Fatal(err)
	}
	p := len(c.Predictors)
	w, _ := fit.RowWeight(recs[0])
	if hb, _ := d.Leverage(rowOf(c.Predictors, c.Rows[0][:p])); !oracleClose(hb, LeverageForRow(fit, c.Rows[0][:p], w)/w, 1e-12) {
		t.Errorf("ridge+vcov bound h = %v, refit %v (the sandwich must not be read)", hb, LeverageForRow(fit, c.Rows[0][:p], w)/w)
	}
	b, err := model.Bind("m", spec, res)
	if err != nil {
		t.Fatal(err)
	}
	b.Result.PenalizedInverse = nil
	if _, err := NewBoundInfluence(b); err == nil {
		t.Error("a penalized fit without its carriage resolves a design")
	}
}

// TestBoundPenalizedLeverage_ProbabilityBasis: under kind probability
// the carried inverse is on the inference basis w* = c·w (c = n_eff/Σw,
// as REG_GLM's prior weights), so the penalized refit's leverage is
// w·c times the bound design's for every row (the GLM refit's glmScale
// convention) — and the inverse is (X̃ᵀWX̃ + Σw·λ₂·P̃)⁻¹/c, not the raw
// weights' inverse. On every weighted oracle case.
func TestBoundPenalizedLeverage_ProbabilityBasis(t *testing.T) {
	t.Parallel()

	n := 0
	for _, c := range loadOLSPenaltyReference(t) {
		if !c.Weighted {
			continue
		}
		n++
		t.Run(c.Name, func(t *testing.T) {
			recs := c.records()
			spec := c.spec()
			spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
			res, err := newOLS(t, spec).FitBuffered(recs)
			if err != nil {
				t.Fatal(err)
			}
			d := boundLeverage(t, spec, res)
			fit, err := FitForAttribute(spec, recs, true)
			if err != nil {
				t.Fatal(err)
			}
			scale := res.NEff / res.SumWeights
			if !(scale > 0) || scale == 1 {
				t.Fatalf("vacuous: basis scale %v", scale)
			}
			p := len(c.Predictors)
			for i, row := range c.Rows {
				w, ok := fit.RowWeight(recs[i])
				if !ok {
					t.Fatalf("row %d excluded", i)
				}
				hb, _ := d.Leverage(rowOf(c.Predictors, row[:p]))
				if hr := LeverageForRow(fit, row[:p], w); !oracleClose(hr, w*scale*hb, 1e-12) {
					t.Errorf("row %d: refit h = %.17g, w·c·bound h = %.17g", i, hr, w*scale*hb)
				}
			}
		})
	}
	if n == 0 {
		t.Fatal("oracle has no weighted case")
	}
}
