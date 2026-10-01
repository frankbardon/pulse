package regression

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestRegOLS_Ridge_ClosedForm asserts the Cholesky-based ridge solver
// reproduces the closed-form reference β = (M2_xx + n·λ·I)⁻¹ · M2_xy
// to machine precision on a small, hand-built fixture.
//
// Fixture: 5 observations, 2 predictors. Designed full-rank so the
// unpenalized OLS path also succeeds, letting us check the Alpha=0
// reduction in a sibling test.
//
//	X = [[1,2], [3,5], [5,8], [2,1], [4,6]]
//	y = [3, 8, 13, 3, 10]
//
// μ_x = (3.0, 4.4); μ_y = 7.4.
// M2_xx = [[10, 17], [17, 33.2]]; M2_xy = [27, 50.2].
//
// reference: numpy.linalg.solve(M2_xx + n·alpha·I, M2_xy) reproduces
// each (alpha, β) triple below to ≥ 1e-15. Values precomputed via the
// equivalent gonum solver pipeline used by solveRidge itself so the
// fixture is independent of any external dependency.
//
//	alpha = 0.5: β = [0.7027027027027037, 1.0715421303656590]
//	             intercept = 0.5771065182829892
//	alpha = 1.0: β = [0.6267605633802815, 1.0352112676056338]
//	             intercept = 0.96478873239436735
//	alpha = 2.0: β = [0.5443478260869564, 0.9478260869565217]
//	             intercept = 1.5965217391304356
func TestRegOLS_Ridge_ClosedForm(t *testing.T) {
	predictors := []string{"x1", "x2"}
	X := [][]float64{{1, 2}, {3, 5}, {5, 8}, {2, 1}, {4, 6}}
	y := []float64{3, 8, 13, 3, 10}
	records := recordsFromPairs(predictors, X, y)

	cases := []struct {
		alpha             float64
		wantBeta          []float64
		wantIntercept     float64
		wantR2NotNegative bool
	}{
		{
			alpha:             0.5,
			wantBeta:          []float64{0.7027027027027037, 1.0715421303656590},
			wantIntercept:     0.5771065182829892,
			wantR2NotNegative: true,
		},
		{
			alpha:             1.0,
			wantBeta:          []float64{0.6267605633802815, 1.0352112676056338},
			wantIntercept:     0.96478873239436735,
			wantR2NotNegative: true,
		},
		{
			alpha:             2.0,
			wantBeta:          []float64{0.5443478260869564, 0.9478260869565217},
			wantIntercept:     1.5965217391304356,
			wantR2NotNegative: true,
		},
	}

	for _, c := range cases {
		t.Run("alpha="+ftoa(c.alpha), func(t *testing.T) {
			spec := &types.RegressionSpec{
				Type: types.REG_OLS, Target: "y", Predictors: predictors,
				Penalty: "l2", Alpha: c.alpha,
			}
			eng := newOLS(t, spec)
			res, err := eng.FitBuffered(records)
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			for i, name := range predictors {
				if !closef(res.Coefficients[name], c.wantBeta[i], 1e-10) {
					t.Errorf("β_%s = %v, want %v", name, res.Coefficients[name], c.wantBeta[i])
				}
			}
			if !closef(res.Coefficients[InterceptKey], c.wantIntercept, 1e-10) {
				t.Errorf("intercept = %v, want %v", res.Coefficients[InterceptKey], c.wantIntercept)
			}
			if res.Penalty != "l2" {
				t.Errorf("result Penalty = %q, want l2", res.Penalty)
			}
			if !closef(res.Alpha, c.alpha, 0) {
				t.Errorf("result Alpha = %v, want %v", res.Alpha, c.alpha)
			}
			if res.ConvergedIters != 0 {
				t.Errorf("ridge is closed-form, ConvergedIters = %d, want 0", res.ConvergedIters)
			}
			// SE / p-value entries populated for every predictor + intercept.
			for _, name := range append([]string{InterceptKey}, predictors...) {
				if _, ok := res.StdErrors[name]; !ok {
					t.Errorf("StdErrors[%q] missing", name)
				}
				if _, ok := res.PValues[name]; !ok {
					t.Errorf("PValues[%q] missing", name)
				}
			}
			if c.wantR2NotNegative && res.R2 < 0 {
				t.Errorf("R² = %v, want ≥ 0", res.R2)
			}
		})
	}
}

// TestRegOLS_Ridge_AlphaZeroReducesToOLS asserts the Alpha=0 boundary:
// solveRidge with alpha=0 must produce coefficients identical (within
// 1e-8) to the unpenalized solveOLS path on the same input. The Phase 2
// dispatch table rejects Alpha=0 with Penalty set via ValidateRegression,
// so this test calls solveRidge directly to exercise the math.
func TestRegOLS_Ridge_AlphaZeroReducesToOLS(t *testing.T) {
	predictors := []string{"x1", "x2"}
	X := [][]float64{{1, 2}, {3, 5}, {5, 8}, {2, 1}, {4, 6}}
	y := []float64{3, 8, 13, 3, 10}

	// Buffered unpenalized fit (baseline).
	baselineSpec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictors}
	baselineEng := newOLS(t, baselineSpec)
	baseline, err := baselineEng.FitBuffered(recordsFromPairs(predictors, X, y))
	if err != nil {
		t.Fatalf("baseline FitBuffered: %v", err)
	}

	// Direct solveRidge(alpha=0) — bypasses spec validation so we can
	// exercise the closed-form math at the boundary.
	acc := newOLSAccumulator(2)
	for i := range y {
		acc.UpdateRow(X[i], y[i])
	}
	ridge, err := solveRidge(acc, 0)
	if err != nil {
		t.Fatalf("solveRidge(alpha=0): %v", err)
	}

	for i, name := range predictors {
		if !closef(baseline.Coefficients[name], ridge.Coefficients[i], 1e-8) {
			t.Errorf("β_%s baseline=%v ridge(α=0)=%v", name, baseline.Coefficients[name], ridge.Coefficients[i])
		}
	}
	if !closef(baseline.Coefficients[InterceptKey], ridge.Intercept, 1e-8) {
		t.Errorf("intercept baseline=%v ridge(α=0)=%v", baseline.Coefficients[InterceptKey], ridge.Intercept)
	}
	if !closef(baseline.R2, ridge.R2, 1e-10) {
		t.Errorf("R² baseline=%v ridge(α=0)=%v", baseline.R2, ridge.R2)
	}
}

// TestRegOLS_Ridge_RankDeficientBoundary checks that ridge regularization
// rescues a perfectly-collinear design that unpenalized OLS would
// reject. Two predictors with x2 = 2·x1 yield a singular centered Gram;
// adding nλI makes it positive-definite.
func TestRegOLS_Ridge_RankDeficientBoundary(t *testing.T) {
	predictors := []string{"x1", "x2"}
	records := make([]Record, 10)
	for i := 0; i < 10; i++ {
		x1 := float64(i)
		x2 := 2 * x1
		records[i] = mockRecord{values: map[string]float64{
			"x1": x1, "x2": x2, "y": 1 + x1,
		}}
	}

	// Unpenalized OLS rejects this with RANK_DEFICIENT.
	unpenSpec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictors}
	unpenEng := newOLS(t, unpenSpec)
	_, err := unpenEng.FitBuffered(records)
	if !errors.HasCode(err, errors.PROCESSING_REGRESSION_RANK_DEFICIENT) {
		t.Fatalf("unpenalized fit on collinear data: err=%v, want RANK_DEFICIENT", err)
	}

	// Ridge with positive alpha succeeds.
	ridgeSpec := &types.RegressionSpec{
		Type: types.REG_OLS, Target: "y", Predictors: predictors,
		Penalty: "l2", Alpha: 1.0,
	}
	ridgeEng := newOLS(t, ridgeSpec)
	res, err := ridgeEng.FitBuffered(records)
	if err != nil {
		t.Fatalf("ridge FitBuffered: %v", err)
	}
	// Coefficients should split the signal across the two collinear
	// columns; absolute values not asserted (sensitive to alpha), but R²
	// should be high since the true relationship lives entirely within
	// the column space of the design.
	if res.R2 < 0.9 {
		t.Errorf("R² = %v, want ≥ 0.9 on this signal", res.R2)
	}
}

// ftoa formats a float for subtest names.
func ftoa(v float64) string {
	return fmt.Sprintf("%v", v)
}
