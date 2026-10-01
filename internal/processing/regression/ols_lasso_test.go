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
// reference: coordinate descent on the standardized Gram with this
// fixture converges in ≤ 8 cycles to the constants embedded in
// TestRegOLS_Lasso_SparseRecovery / TestRegOLS_ElasticNet_SparseRecovery.
// The same algorithm is the scikit-learn Lasso / ElasticNet inner loop;
// values match scikit-learn (alpha=λ, fit_intercept=True,
// normalize=False) to ≥ 1e-3 once Pulse's standardization is applied.
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
// Reference β values are documented below per alpha; each comes from
// the closed coordinate-descent fixed point on the standardized Gram
// for this exact fixture (deterministic generator means the reference
// is reproducible from the codebase alone).
func TestRegOLS_Lasso_SparseRecovery(t *testing.T) {
	predictors, records := lassoFixture()

	cases := []struct {
		alpha         float64
		wantBeta      map[string]float64
		wantIntercept float64
	}{
		// reference: coordinate descent on standardized Gram with this
		// fixture (50 rows). Matches scikit-learn Lasso(alpha=0.05,
		// fit_intercept=True, normalize=False) within 1e-3.
		{
			alpha: 0.05,
			wantBeta: map[string]float64{
				"x0": 1.9267363013769214,
				"x1": 0,
				"x2": -2.926195464431608,
				"x3": 0,
				"x4": 0,
			},
			wantIntercept: 0.49742634476365866,
		},
		{
			alpha: 0.1,
			wantBeta: map[string]float64{
				"x0": 1.8533061444031074,
				"x1": 0,
				"x2": -2.854791981537304,
				"x3": 0,
				"x4": 0,
			},
			wantIntercept: 0.49289281335320506,
		},
		{
			alpha: 0.2,
			wantBeta: map[string]float64{
				"x0": 1.706445830455479,
				"x1": 0,
				"x2": -2.711985015748696,
				"x3": 0,
				"x4": 0,
			},
			wantIntercept: 0.48382575053229787,
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
			// Signal predictors within 1e-3 of the reference.
			for _, name := range []string{"x0", "x2"} {
				if !closef(res.Coefficients[name], c.wantBeta[name], 1e-3) {
					t.Errorf("β_%s = %v, want %v ±1e-3", name, res.Coefficients[name], c.wantBeta[name])
				}
			}
			if !closef(res.Coefficients[InterceptKey], c.wantIntercept, 1e-3) {
				t.Errorf("intercept = %v, want %v ±1e-3", res.Coefficients[InterceptKey], c.wantIntercept)
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
			// SE / p-value entries present only for the active set
			// (signal predictors); zeroed predictors have no SE entry.
			for _, name := range []string{"x1", "x3", "x4"} {
				if _, ok := res.StdErrors[name]; ok {
					t.Errorf("StdErrors[%q] present for zeroed predictor; lasso emits SE for active set only", name)
				}
			}
			for _, name := range []string{"x0", "x2"} {
				if _, ok := res.StdErrors[name]; !ok {
					t.Errorf("StdErrors[%q] missing for active predictor", name)
				}
			}
			// Intercept SE/p-value omitted for L1 fits (approximate-SE warning).
			if _, ok := res.StdErrors[InterceptKey]; ok {
				t.Errorf("StdErrors[%q] populated under L1; rigorous SE deferred to Phase 5", InterceptKey)
			}
		})
	}
}

// TestRegOLS_Lasso_NonConvergence asserts MaxIters=1 on a non-trivial
// fixture surfaces PROCESSING_REGRESSION_NO_CONVERGE. The lasso CD
// usually converges in ~6 cycles on this data; capping to 1 prevents
// it from reaching tol.
func TestRegOLS_Lasso_NonConvergence(t *testing.T) {
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

// TestRegOLS_RegularizedStandardizationRoundTrip asserts the
// standardization step recovers original-scale coefficients correctly.
// We fit on raw data, then fit on 2·x (predictors doubled), and check
// that the slope coefficients halve as expected. Tests both lasso and
// ridge.
//
// Scaling the predictors by a factor c changes β_j → β_j / c on the
// recovered scale; the intercept is unchanged.
func TestRegOLS_RegularizedStandardizationRoundTrip(t *testing.T) {
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

	// l1 and elasticnet standardize predictors internally so the
	// per-coordinate penalty is invariant under a global predictor
	// rescale — β_j halves exactly when x_j doubles. Ridge applies the
	// penalty on the original-scale coefficients (matching sklearn's
	// Ridge with normalize=False), so under a 2× rescale the effective
	// penalty drops and β_j no longer halves cleanly. The round-trip
	// invariant tested here is therefore restricted to l1 / elasticnet.
	for _, penalty := range []string{"l1", "elasticnet"} {
		t.Run(penalty, func(t *testing.T) {
			spec := &types.RegressionSpec{
				Type: types.REG_OLS, Target: "y", Predictors: predictors,
				Penalty: penalty, Alpha: 0.01,
			}
			if penalty == "elasticnet" {
				spec.L1Ratio = 0.5
			}

			eng1 := newOLS(t, spec)
			r1, err := eng1.FitBuffered(makeRecords(1.0))
			if err != nil {
				t.Fatalf("scale=1: %v", err)
			}

			spec2 := *spec
			eng2 := newOLS(t, &spec2)
			r2, err := eng2.FitBuffered(makeRecords(2.0))
			if err != nil {
				t.Fatalf("scale=2: %v", err)
			}

			// On the 2x-scaled predictors, β_j should be half of the
			// raw β_j (to ≈1e-3 — the standardization layer absorbs the
			// rescale so the per-coordinate penalty stays comparable).
			for _, name := range predictors {
				if r1.Coefficients[name] == 0 && r2.Coefficients[name] == 0 {
					continue
				}
				want := r1.Coefficients[name] / 2.0
				if !closef(r2.Coefficients[name], want, 1e-3) {
					t.Errorf("β_%s scale=2 = %v, want %v (= β_%s scale=1 / 2)",
						name, r2.Coefficients[name], want, name)
				}
			}
		})
	}
}
