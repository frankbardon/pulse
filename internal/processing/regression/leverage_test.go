package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestReferenceOracle_PenalizedLeverageMatchesClosedForm: the refit's
// leverage on a penalised REG_OLS — ridge (the exact hat
// diag(X(XᵀWX + Σw·α·P)⁻¹XᵀW)), lasso (the hat of the unpenalised OLS on
// the active set) and elastic net (the active-set hat with its
// α·(1 − l1_ratio) ridge term), unweighted, frequency-weighted and with
// unpenalised predictors — matches R matrix algebra on the uncentred
// design (gen_ols_penalty.R penalized_hat) at relative 1e-10 on every
// row. On the l1 / elastic-net cases the hat reads exactly glmnet's df
// active columns, and on an unweighted lasso its trace is 1 + |active|
// (Zou–Hastie–Tibshirani df(lasso)).
func TestReferenceOracle_PenalizedLeverageMatchesClosedForm(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, c := range loadOLSPenaltyReference(t) {
		t.Run(c.Name, func(t *testing.T) {
			if len(c.Leverage) != len(c.Rows) {
				t.Fatalf("golden has %d leverage values for %d rows", len(c.Leverage), len(c.Rows))
			}
			seen[c.Penalty] = true
			recs := c.records()
			fit, err := FitForAttribute(c.spec(), recs, true)
			if err != nil {
				t.Fatalf("FitForAttribute: %v", err)
			}
			if c.Penalty != "l2" {
				if c.DFGlmnet == nil {
					t.Fatal("golden lacks df_glmnet on an l1 / elasticnet case")
				}
				if got := len(fit.design.Cols); float64(got) != *c.DFGlmnet {
					t.Errorf("active columns = %d, glmnet df %v", got, *c.DFGlmnet)
				}
			}
			p := len(c.Predictors)
			trace := 0.0
			for i, row := range c.Rows {
				w, ok := fit.RowWeight(recs[i])
				if !ok {
					t.Fatalf("row %d excluded from the refit", i)
				}
				h := LeverageForRow(fit, row[:p], w)
				trace += h
				if !oracleClose(h, c.Leverage[i], 1e-10) {
					t.Errorf("h[%d] = %.17g, oracle %.17g", i, h, c.Leverage[i])
				}
			}
			if c.Penalty == "l1" && !c.Weighted {
				if want := 1 + float64(len(fit.design.Cols)); math.Abs(trace-want) > 1e-10 {
					t.Errorf("Σh = %.17g, want 1 + |active| = %v", trace, want)
				}
			}
		})
	}
	for _, want := range []string{"l1", "l2", "elasticnet"} {
		if !seen[want] {
			t.Errorf("golden lacks a %s case", want)
		}
	}
}

// TestPenalizedLeverage_EmptyActiveSet: a lasso strong enough to zero
// every slope leaves the intercept-only hat wᵢ/Σw on every row — 1/n
// unweighted — never 0 (a refit without a design).
func TestPenalizedLeverage_EmptyActiveSet(t *testing.T) {
	t.Parallel()

	var recs []Record
	for i := 0; i < 20; i++ {
		x := float64(i % 7)
		recs = append(recs, mockRecord{values: map[string]float64{"x": x, "y": float64(i % 3)}})
	}
	spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 1e6}
	fit, err := FitForAttribute(spec, recs, true)
	if err != nil {
		t.Fatalf("FitForAttribute: %v", err)
	}
	if fit.Coefficients[0] != 0 {
		t.Fatalf("β = %v, want the lasso to zero it", fit.Coefficients[0])
	}
	for i := 0; i < 3; i++ {
		if h := LeverageForRow(fit, []float64{float64(i)}, 1); h != 1.0/20 {
			t.Errorf("h(x=%d) = %v, want 1/20", i, h)
		}
	}
}

// TestPenalizedLeverage_RidgeShrinksTowardsIntercept: the unpenalized
// refit's hat has trace p + 1, and a ridge hat on the same rows stays
// at or above 1/n per row with a smaller trace (its effective df) —
// the penalty shrinks every row's leverage towards the intercept-only
// hat.
func TestPenalizedLeverage_RidgeShrinksTowardsIntercept(t *testing.T) {
	t.Parallel()

	c := loadOLSPenaltyReference(t)[0]
	recs := c.records()
	base := c.spec()
	base.Penalty, base.Alpha, base.L1Ratio = "", 0, 0
	ols, err := FitForAttribute(base, recs, true)
	if err != nil {
		t.Fatal(err)
	}
	ridge := c.spec()
	ridge.Penalty, ridge.Alpha, ridge.L1Ratio = "l2", 10, 0
	rfit, err := FitForAttribute(ridge, recs, true)
	if err != nil {
		t.Fatal(err)
	}
	p := len(c.Predictors)
	sumOLS, sumRidge := 0.0, 0.0
	for _, row := range c.Rows {
		sumOLS += LeverageForRow(ols, row[:p], 1)
		hr := LeverageForRow(rfit, row[:p], 1)
		if !(hr >= 1.0/float64(len(c.Rows))) {
			t.Fatalf("ridge h = %v below 1/n", hr)
		}
		sumRidge += hr
	}
	if math.Abs(sumOLS-float64(p+1)) > 1e-9 {
		t.Errorf("OLS Σh = %v, want p + 1 = %d", sumOLS, p+1)
	}
	if !(sumRidge < sumOLS) {
		t.Errorf("ridge Σh = %v, want below OLS's %v (shrinkage lowers df)", sumRidge, sumOLS)
	}
}
