package regression

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// lassoFixture builds the 50-row, 5-predictor sparse-recovery fixture
// shared by the lasso and elasticnet tests. The data-generating process
// is:
//
//	X_i = (sin(0.13i), cos(0.27i), sin(0.41i+0.5), cos(0.55i+1.3), sin(0.71i+2.1))
//	y_i = 0.5 + 2·X_i[0] − 3·X_i[2] + 0.05·sin(0.93i+0.7)
//
// so the true coefficient vector is (2, 0, −3, 0, 0). A correct lasso
// fit must zero out predictors 1, 3, 4 and recover predictors 0 and 2
// near (2, −3). The intercept lies near 0.5.
//
// reference: R glmnet(standardize = FALSE) on the unit-SD response
// (scripts/reference/gen_ols_penalty.R, KKT-checked at 1e-10; the full
// pin is TestRegOLS_PenaltyMatchesGlmnet) gives the constants embedded
// in TestRegOLS_Lasso_SparseRecovery / TestRegOLS_ElasticNet_SparseRecovery.
func lassoFixture() ([]string, []Record) {
	predictors := []string{"x0", "x1", "x2", "x3", "x4"}
	n := 50
	records := make([]Record, n)
	for i := 0; i < n; i++ {
		x0 := math.Sin(0.13 * float64(i))
		x1 := math.Cos(0.27 * float64(i))
		x2 := math.Sin(0.41*float64(i) + 0.5)
		x3 := math.Cos(0.55*float64(i) + 1.3)
		x4 := math.Sin(0.71*float64(i) + 2.1)
		noise := 0.05 * math.Sin(0.93*float64(i)+0.7)
		y := 0.5 + 2.0*x0 - 3.0*x2 + noise
		records[i] = mockRecord{values: map[string]float64{
			"x0": x0, "x1": x1, "x2": x2, "x3": x3, "x4": x4, "y": y,
		}}
	}
	return predictors, records
}

// TestRegOLS_Lasso_SparseRecovery asserts the L1 solver
// (a) zeros out the three noise predictors exactly, and
// (b) recovers the two signal predictors near their true values.
//
// Reference β values per alpha: the glmnet oracle's (lassoFixture),
// asserted at the default tolerance.
func TestRegOLS_Lasso_SparseRecovery(t *testing.T) {
	t.Parallel()

	predictors, records := lassoFixture()

	cases := []struct {
		alpha         float64
		wantBeta      map[string]float64
		wantIntercept float64
	}{
		// reference: glmnet(standardize = FALSE), gen_ols_penalty.R.
		{
			alpha: 0.05,
			wantBeta: map[string]float64{
				"x0": 1.894618887569843,
				"x1": 0,
				"x2": -2.8976837930255774,
				"x3": 0,
				"x4": 0,
			},
			wantIntercept: 0.4956201318005414,
		},
		{
			alpha: 0.1,
			wantBeta: map[string]float64{
				"x0": 1.7890713167889487,
				"x1": 0,
				"x2": -2.797768638725244,
				"x3": 0,
				"x4": 0,
			},
			wantIntercept: 0.4892803874269709,
		},
		{
			alpha: 0.2,
			wantBeta: map[string]float64{
				"x0": 1.5779761752271606,
				"x1": 0,
				"x2": -2.5979383301245758,
				"x3": 0,
				"x4": 0,
			},
			wantIntercept: 0.47660089867982985,
		},
	}

	for _, c := range cases {
		t.Run("alpha="+ftoa(c.alpha), func(t *testing.T) {
			spec := &types.RegressionSpec{
				Type: types.REG_OLS, Target: "y", Predictors: predictors,
				Penalty: "l1", Alpha: c.alpha,
			}
			eng := newOLS(t, spec)
			res, err := eng.FitBuffered(records)
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			// Noise predictors zero exactly.
			for _, name := range []string{"x1", "x3", "x4"} {
				if res.Coefficients[name] != 0 {
					t.Errorf("noise predictor %s coefficient = %v, want exactly 0", name, res.Coefficients[name])
				}
			}
			// Signal predictors within 1e-6 of the reference.
			for _, name := range []string{"x0", "x2"} {
				if !closef(res.Coefficients[name], c.wantBeta[name], 1e-6) {
					t.Errorf("β_%s = %v, want %v ±1e-6", name, res.Coefficients[name], c.wantBeta[name])
				}
			}
			if !closef(res.Coefficients[InterceptKey], c.wantIntercept, 1e-6) {
				t.Errorf("intercept = %v, want %v ±1e-6", res.Coefficients[InterceptKey], c.wantIntercept)
			}
			// Convergence metadata.
			if res.Penalty != "l1" {
				t.Errorf("Penalty = %q, want l1", res.Penalty)
			}
			if !closef(res.Alpha, c.alpha, 0) {
				t.Errorf("Alpha = %v, want %v", res.Alpha, c.alpha)
			}
			if res.ConvergedIters <= 0 {
				t.Errorf("ConvergedIters = %d, want > 0", res.ConvergedIters)
			}
			// No inference: std error, p-value and interval are NaN
			// (null on the wire) on every coefficient — the dropped
			// predictors, the kept ones and the intercept alike.
			assertInferenceNull(t, res, coefficientKeys(predictors))
		})
	}
}

// TestRegOLS_Lasso_NonConvergence asserts MaxIters=1 on a non-trivial
// fixture surfaces PROCESSING_REGRESSION_NO_CONVERGE. The lasso CD
// usually converges in ~6 cycles on this data; capping to 1 prevents
// it from reaching tol.
func TestRegOLS_Lasso_NonConvergence(t *testing.T) {
	t.Parallel()

	predictors, records := lassoFixture()
	spec := &types.RegressionSpec{
		Type: types.REG_OLS, Target: "y", Predictors: predictors,
		Penalty: "l1", Alpha: 0.05, MaxIters: 1, Tol: 1e-10,
	}
	eng := newOLS(t, spec)
	_, err := eng.FitBuffered(records)
	if err == nil {
		t.Fatal("FitBuffered: nil error; want PROCESSING_REGRESSION_NO_CONVERGE")
	}
	if !errors.HasCode(err, errors.PROCESSING_REGRESSION_NO_CONVERGE) {
		t.Errorf("err = %v, want PROCESSING_REGRESSION_NO_CONVERGE", err)
	}
}

// TestRegOLS_PenaltyReadsPredictorUnits: the penalty reads each
// coefficient on its predictor's own units (glmnet standardize =
// FALSE). Doubling the predictors halves the coefficients only when the
// strength follows the units: a lasso on 2·x at α is the lasso on x at
// α/2 with every slope halved (the objective is the same function), and
// the same α on 2·x is a weaker penalty than on x.
func TestRegOLS_PenaltyReadsPredictorUnits(t *testing.T) {
	t.Parallel()

	predictors := []string{"x0", "x1"}
	n := 40
	makeRecords := func(scale float64) []Record {
		recs := make([]Record, n)
		for i := 0; i < n; i++ {
			x0 := scale * math.Sin(0.13*float64(i))
			x1 := scale * math.Cos(0.27*float64(i)+0.4)
			noise := 0.02 * math.Sin(0.71*float64(i)+0.9)
			y := 1.0 + 2.5*(x0/scale) - 1.2*(x1/scale) + noise
			recs[i] = mockRecord{values: map[string]float64{
				"x0": x0, "x1": x1, "y": y,
			}}
		}
		return recs
	}
	fit := func(alpha, scale float64) *types.RegressionResult {
		spec := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: predictors,
			Penalty: "l1", Alpha: alpha, Tol: 1e-15, MaxIters: 1000000}
		r, err := newOLS(t, spec).FitBuffered(makeRecords(scale))
		if err != nil {
			t.Fatalf("α=%v scale=%v: %v", alpha, scale, err)
		}
		return r
	}
	const alpha = 0.05
	raw, doubled, weaker := fit(alpha/2, 1), fit(alpha, 2), fit(alpha, 1)
	for _, name := range predictors {
		if want := raw.Coefficients[name] / 2; !oracleClose(doubled.Coefficients[name], want, 1e-10) {
			t.Errorf("β_%s on 2·x at α = %.17g, want (β on x at α/2)/2 = %.17g", name, doubled.Coefficients[name], want)
		}
		if 2*doubled.Coefficients[name] == weaker.Coefficients[name] {
			t.Errorf("β_%s: the same α on 2·x and x gave the same fit; the penalty ignored the units", name)
		}
	}
}
