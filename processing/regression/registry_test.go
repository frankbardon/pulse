package regression

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestRegistry_AllTypesRegistered asserts every RegressionType returned by
// types.AllRegressionTypes() has a factory in the local registry. Catches
// the "added REG_* to types but forgot the init() registration" drift.
func TestRegistry_AllTypesRegistered(t *testing.T) {
	registered := map[types.RegressionType]bool{}
	for _, rt := range RegisteredTypes() {
		registered[rt] = true
	}
	for _, rt := range types.AllRegressionTypes() {
		if !registered[rt] {
			t.Errorf("regression type %s not registered", rt)
		}
	}
	if len(registered) != len(types.AllRegressionTypes()) {
		t.Errorf("registered count = %d, want %d", len(registered), len(types.AllRegressionTypes()))
	}
}

// TestFit_ModifierContracts asserts the modifier dispatch contracts.
// Phase 5 retired the not-implemented stub for OLS / GLM modifier
// variants; Bayes + any modifier is now rejected at validation. The
// table covers every remaining surface: modifier OLS / GLM specs that
// reach the registry but receive no records, and Bayes specs that
// the validator rejects before construction.
func TestFit_ModifierContracts(t *testing.T) {
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

// TestBuild_UnknownType asserts Build surfaces PROCESSING_CONFIG when a
// spec references an unregistered RegressionType.
func TestBuild_UnknownType(t *testing.T) {
	spec := &types.RegressionSpec{Type: types.RegressionType("REG_NOT_A_REAL_TYPE"), Target: "y"}
	_, err := Build([]*types.RegressionSpec{spec}, nil)
	if err == nil {
		t.Fatal("Build with unknown type returned nil error")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("Build error code = %v, want PROCESSING_CONFIG", err)
	}
}

// TestFit_EmptySpecs asserts Fit returns (nil, nil) when no specs are
// provided — the orchestrator can call Fit unconditionally.
func TestFit_EmptySpecs(t *testing.T) {
	out, err := Fit(nil, nil)
	if err != nil {
		t.Fatalf("Fit(nil) error: %v", err)
	}
	if out != nil {
		t.Errorf("Fit(nil) returned %v, want nil slice", out)
	}
}
