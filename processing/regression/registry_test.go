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

// TestFit_NotImplemented asserts every still-stubbed registered factory
// returns PROCESSING_REGRESSION_NOT_IMPLEMENTED from Engine.Fit. Phase 0
// covered every operator; Phase 1 retired the stub for unpenalized
// REG_OLS; Phase 2 retired the stub for regularized REG_OLS. The remaining
// stubs are REG_GLM (Phase 3), REG_BAYES_LINEAR (Phase 4), and
// modifier-wrapped REG_OLS variants (Phase 5).
func TestFit_NotImplemented(t *testing.T) {
	cases := []struct {
		name string
		spec *types.RegressionSpec
	}{
		{name: "REG_GLM (Phase 3)", spec: &types.RegressionSpec{Type: types.REG_GLM, Target: "y", Predictors: []string{"x"}, Family: "binomial"}},
		{name: "REG_BAYES_LINEAR (Phase 4)", spec: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: []string{"x"}}},
		{name: "REG_OLS bootstrap modifier (Phase 5)", spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Resample: "bootstrap"}},
		{name: "REG_OLS stepwise selection (Phase 5)", spec: &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Selection: "stepwise", Criterion: "aic"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Fit([]*types.RegressionSpec{c.spec}, nil)
			if err == nil {
				t.Fatalf("Fit returned nil error; want PROCESSING_REGRESSION_NOT_IMPLEMENTED")
			}
			if !errors.HasCode(err, errors.PROCESSING_REGRESSION_NOT_IMPLEMENTED) {
				t.Errorf("Fit error code = %v, want PROCESSING_REGRESSION_NOT_IMPLEMENTED", err)
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
