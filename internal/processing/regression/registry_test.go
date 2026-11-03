package regression

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestFit_ModifierContracts asserts the modifier dispatch contracts:
// modifier OLS specs that reach the registry but receive no records,
// and Bayes + any modifier, which the validator rejects before
// construction.
func TestFit_ModifierContracts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		spec     *types.RegressionSpec
		wantCode errors.Code
	}{
		{name: "REG_OLS bootstrap modifier", spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Resample: "bootstrap"}, wantCode: errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA},
		{name: "REG_OLS stepwise selection", spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Selection: "stepwise", Criterion: "aic"}, wantCode: errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA},
		{name: "REG_BAYES_LINEAR + bootstrap rejected at validation", spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Resample: "bootstrap"}, wantCode: errors.PROCESSING_CONFIG},
		{name: "REG_BAYES_LINEAR + stepwise rejected at validation", spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}, Selection: "stepwise", Criterion: "aic"}, wantCode: errors.PROCESSING_CONFIG},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Fit([]*types.RegressionSpec{c.spec}, nil)
			if err == nil {
				t.Fatalf("Fit returned nil error; want %v", c.wantCode)
			}
			if !errors.HasCode(err, c.wantCode) {
				t.Errorf("Fit error code = %v, want %v", err, c.wantCode)
			}
		})
	}
}

// TestBuild_UnknownType asserts Build surfaces predict's
// PULSE_REGRESSION_INVALID refusal when a spec references an
// unregistered RegressionType.
func TestBuild_UnknownType(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: types.RegressionType("REG_NOT_A_REAL_TYPE"), Target: "y"}
	_, err := Build([]*types.RegressionSpec{spec}, nil)
	if err == nil {
		t.Fatal("Build with unknown type returned nil error")
	}
	if !errors.HasCode(err, errors.PULSE_REGRESSION_INVALID) ||
		err.Error() != "PULSE_REGRESSION_INVALID: regression[0]: unknown regression type REG_NOT_A_REAL_TYPE" {
		t.Errorf("Build error = %v, want PULSE_REGRESSION_INVALID regression[0]", err)
	}
}

// TestFit_EmptySpecs asserts Fit returns (nil, nil) when no specs are
// provided — the orchestrator can call Fit unconditionally.
func TestFit_EmptySpecs(t *testing.T) {
	t.Parallel()

	out, err := Fit(nil, nil)
	if err != nil {
		t.Fatalf("Fit(nil) error: %v", err)
	}
	if out != nil {
		t.Errorf("Fit(nil) returned %v, want nil slice", out)
	}
}
