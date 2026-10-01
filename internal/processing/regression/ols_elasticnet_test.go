package regression

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestRegOLS_ElasticNet_SparseRecovery asserts the elasticnet solver
// recovers the sparse signal in the same fixture used by the lasso
// test (predictors x0, x2 carry signal at (2, −3); x1, x3, x4 are
// noise). With L1Ratio=0.5 the L1 term still drives the noise
// coefficients to exactly zero; the active-set coefficients shrink
// slightly relative to the pure-lasso case due to the L2 contribution.
//
// reference: coordinate descent on the standardized Gram for this
// deterministic fixture; the same algorithm matches scikit-learn
// ElasticNet(alpha=λ, l1_ratio=0.5, fit_intercept=True,
// normalize=False) to ≥ 1e-3 once standardization is applied.
//
//	alpha = 0.05, L1Ratio = 0.5:
//	β = [1.9140413033823895, 0, -2.8886851723372433, 0, 0]
//	intercept = 0.49500733818430903
//	alpha = 0.10, L1Ratio = 0.5:
//	β = [1.8321241669615214, 0, -2.7850384639746646, 0, 0]
//	intercept = 0.4883909625313358
func TestRegOLS_ElasticNet_SparseRecovery(t *testing.T) {
	predictors, records := lassoFixture()

	cases := []struct {
		alpha         float64
		l1Ratio       float64
		wantBeta      map[string]float64
		wantIntercept float64
	}{
		{
			alpha:   0.05,
			l1Ratio: 0.5,
			wantBeta: map[string]float64{
				"x0": 1.9140413033823895,
				"x1": 0,
				"x2": -2.8886851723372433,
				"x3": 0,
				"x4": 0,
			},
			wantIntercept: 0.49500733818430903,
		},
		{
			alpha:   0.10,
			l1Ratio: 0.5,
			wantBeta: map[string]float64{
				"x0": 1.8321241669615214,
				"x1": 0,
				"x2": -2.7850384639746646,
				"x3": 0,
				"x4": 0,
			},
			wantIntercept: 0.4883909625313358,
		},
	}

	for _, c := range cases {
		t.Run("alpha="+ftoa(c.alpha)+"_l1r="+ftoa(c.l1Ratio), func(t *testing.T) {
			spec := &types.RegressionSpec{
				Type: types.REG_OLS, Target: "y", Predictors: predictors,
				Penalty: "elasticnet", Alpha: c.alpha, L1Ratio: c.l1Ratio,
			}
			eng := newOLS(t, spec)
			res, err := eng.FitBuffered(records)
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			for _, name := range []string{"x1", "x3", "x4"} {
				if res.Coefficients[name] != 0 {
					t.Errorf("noise predictor %s coefficient = %v, want exactly 0", name, res.Coefficients[name])
				}
			}
			for _, name := range []string{"x0", "x2"} {
				if !closef(res.Coefficients[name], c.wantBeta[name], 1e-3) {
					t.Errorf("β_%s = %v, want %v ±1e-3", name, res.Coefficients[name], c.wantBeta[name])
				}
			}
			if !closef(res.Coefficients[InterceptKey], c.wantIntercept, 1e-3) {
				t.Errorf("intercept = %v, want %v ±1e-3", res.Coefficients[InterceptKey], c.wantIntercept)
			}
			if res.Penalty != "elasticnet" {
				t.Errorf("Penalty = %q, want elasticnet", res.Penalty)
			}
			if !closef(res.Alpha, c.alpha, 0) {
				t.Errorf("Alpha = %v, want %v", res.Alpha, c.alpha)
			}
			if !closef(res.L1Ratio, c.l1Ratio, 0) {
				t.Errorf("L1Ratio = %v, want %v", res.L1Ratio, c.l1Ratio)
			}
			if res.ConvergedIters <= 0 {
				t.Errorf("ConvergedIters = %d, want > 0", res.ConvergedIters)
			}
		})
	}
}

// TestRegOLS_RegularizedStreaming asserts the streaming orchestrator
// path produces identical results to the buffered path for every
// regularized variant. Phase 2's streamability gate accepts penalized
// OLS — the streaming Gram is the same as the unpenalized path; only
// the finalize-time solver iterates.
func TestRegOLS_RegularizedStreaming(t *testing.T) {
	predictors, records := lassoFixture()

	cases := []struct {
		name string
		spec *types.RegressionSpec
	}{
		{
			name: "ridge alpha=0.5",
			spec: &types.RegressionSpec{
				Type: types.REG_OLS, Target: "y", Predictors: predictors,
				Penalty: "l2", Alpha: 0.5,
			},
		},
		{
			name: "lasso alpha=0.05",
			spec: &types.RegressionSpec{
				Type: types.REG_OLS, Target: "y", Predictors: predictors,
				Penalty: "l1", Alpha: 0.05,
			},
		},
		{
			name: "elasticnet alpha=0.05 l1r=0.5",
			spec: &types.RegressionSpec{
				Type: types.REG_OLS, Target: "y", Predictors: predictors,
				Penalty: "elasticnet", Alpha: 0.05, L1Ratio: 0.5,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Buffered path.
			buf := newOLS(t, c.spec)
			bufRes, err := buf.FitBuffered(records)
			if err != nil {
				t.Fatalf("buffered: %v", err)
			}
			// Streaming path.
			schema := schemaWithFields(append([]string{c.spec.Target}, c.spec.Predictors...)...)
			eng, err := newOLSEngine(c.spec, schema)
			if err != nil {
				t.Fatalf("newOLSEngine: %v", err)
			}
			se, ok := eng.(StreamingEngine)
			if !ok {
				t.Fatalf("streaming engine missing StreamingEngine for %s", c.name)
			}
			for _, rec := range records {
				if err := se.UpdateRow(rec); err != nil {
					t.Fatalf("UpdateRow: %v", err)
				}
			}
			streamRes, err := se.Finalize()
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			// Both paths share the same accumulator and solver so the
			// math is bit-for-bit identical (≤ 1e-12 for ridge, ≤ 1e-10
			// for coordinate descent since the inner loop is sensitive
			// to FP ordering across the two record traversals — but
			// they iterate the same number of cycles on the same Gram).
			const tol = 1e-10
			for _, name := range predictors {
				if !closef(bufRes.Coefficients[name], streamRes.Coefficients[name], tol) {
					t.Errorf("β_%s buf=%v stream=%v Δ=%v",
						name, bufRes.Coefficients[name], streamRes.Coefficients[name],
						bufRes.Coefficients[name]-streamRes.Coefficients[name])
				}
			}
			if !closef(bufRes.Coefficients[InterceptKey], streamRes.Coefficients[InterceptKey], tol) {
				t.Errorf("intercept buf=%v stream=%v",
					bufRes.Coefficients[InterceptKey], streamRes.Coefficients[InterceptKey])
			}
		})
	}
}

// TestCanStreamRequest_RegularizedOLS asserts every regularized OLS
// spec — ridge, lasso, elasticnet — reports streamable at the runtime
// CanStreamRequest gate. This is the runtime side of the Phase 2 flip;
// the descriptor side is covered by predict_streamable_test.go.
//
// CanStreamRequest lives in the parent processing package and we
// cannot import it without an import cycle. The streamability matrix
// in processing/streamability_test.go does this round-trip check
// for the full request → CanStreamRequest path; here we exercise the
// type-level helper that processing.canStream consults.
func TestCanStreamRequest_RegularizedOLS(t *testing.T) {
	cases := []*types.RegressionSpec{
		{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l2", Alpha: 0.1},
		{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1},
		{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5},
	}
	for _, spec := range cases {
		t.Run(spec.Penalty, func(t *testing.T) {
			if !spec.Streamable() {
				t.Errorf("%s.Streamable() = false, want true after Phase 2 flip", spec.Penalty)
			}
		})
	}
}
