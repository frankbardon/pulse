package regression

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestRegOLS_RegularizedSpecValidation table-drives every invalid
// penalty / alpha / l1_ratio combination through ValidateRegression.
// Each row exercises a separate guard rail in spec.go; new rules added
// in later phases extend this table.
func TestRegOLS_RegularizedSpecValidation(t *testing.T) {
	cases := []struct {
		name string
		spec *types.RegressionSpec
		// ok=true means the spec should pass validation.
		ok bool
	}{
		// Positive controls — the validator must not over-reject.
		{
			name: "unpenalized OLS passes",
			spec: &types.RegressionSpec{Type: types.REG_OLS},
			ok:   true,
		},
		{
			name: "ridge with positive alpha passes",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l2", Alpha: 0.1},
			ok:   true,
		},
		{
			name: "lasso with positive alpha passes",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l1", Alpha: 0.1},
			ok:   true,
		},
		{
			name: "elasticnet with 0 < l1_ratio < 1 passes",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5},
			ok:   true,
		},
		{
			// Phase 3: ValidateRegression now branches on Type. REG_GLM
			// has its own validator (validateGLMSpec) — the bare type
			// must still pass when Family is set; OLS-only knobs are
			// ignored on non-GLM types until each engine ships.
			name: "well-formed REG_GLM spec passes",
			spec: &types.RegressionSpec{Type: types.REG_GLM, Family: "binomial"},
			ok:   true,
		},
		{
			// Phase 4: bare REG_BAYES_LINEAR (no prior overrides) defaults
			// every prior parameter and passes validation.
			name: "well-formed REG_BAYES_LINEAR spec passes",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR},
			ok:   true,
		},
		{
			// Phase 4: REG_BAYES_LINEAR with Penalty set is rejected —
			// Penalty is a REG_OLS knob and would silently mislead callers.
			name: "REG_BAYES_LINEAR with stray Penalty rejected",
			spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Penalty: "anything"},
			ok:   false,
		},

		// Unpenalized regressions must not carry Alpha / L1Ratio.
		{
			name: "unpenalized OLS with nonzero Alpha rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Alpha: 0.1},
			ok:   false,
		},
		{
			name: "unpenalized OLS with nonzero L1Ratio rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, L1Ratio: 0.3},
			ok:   false,
		},

		// L1 / L2 require Alpha > 0 and L1Ratio == 0.
		{
			name: "l1 with Alpha=0 rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l1", Alpha: 0},
			ok:   false,
		},
		{
			name: "l1 with negative Alpha rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l1", Alpha: -0.1},
			ok:   false,
		},
		{
			name: "l1 with L1Ratio set rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l1", Alpha: 0.1, L1Ratio: 0.5},
			ok:   false,
		},
		{
			name: "l2 with Alpha=0 rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l2", Alpha: 0},
			ok:   false,
		},
		{
			name: "l2 with L1Ratio set rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l2", Alpha: 0.1, L1Ratio: 0.5},
			ok:   false,
		},

		// Elasticnet requires Alpha > 0 and 0 < L1Ratio < 1.
		{
			name: "elasticnet with Alpha=0 rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "elasticnet", Alpha: 0, L1Ratio: 0.5},
			ok:   false,
		},
		{
			name: "elasticnet with L1Ratio=0 rejected (use l2)",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0},
			ok:   false,
		},
		{
			name: "elasticnet with L1Ratio=1 rejected (use l1)",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 1.0},
			ok:   false,
		},
		{
			name: "elasticnet with L1Ratio>1 rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 1.5},
			ok:   false,
		},
		{
			name: "elasticnet with L1Ratio<0 rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: -0.1},
			ok:   false,
		},

		// Unknown Penalty value rejected.
		{
			name: "unknown penalty rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "ridge", Alpha: 0.1},
			ok:   false,
		},

		// Iteration knobs.
		{
			name: "negative MaxIters rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l1", Alpha: 0.1, MaxIters: -1},
			ok:   false,
		},
		{
			name: "negative Tol rejected",
			spec: &types.RegressionSpec{Type: types.REG_OLS, Penalty: "l1", Alpha: 0.1, Tol: -1},
			ok:   false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateRegression(c.spec)
			if c.ok {
				if err != nil {
					t.Errorf("ValidateRegression(%+v) = %v, want nil", c.spec, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateRegression(%+v) = nil, want PROCESSING_CONFIG", c.spec)
			}
			if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
				t.Errorf("ValidateRegression error code = %v, want PROCESSING_CONFIG", err)
			}
		})
	}
}
