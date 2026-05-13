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

// TestFit_NotImplemented asserts every registered factory returns
// PROCESSING_REGRESSION_NOT_IMPLEMENTED from Engine.Fit. Phase 0
// contract; Phases 1–4 retire the stub per operator.
func TestFit_NotImplemented(t *testing.T) {
	for _, rt := range types.AllRegressionTypes() {
		spec := &types.RegressionSpec{Type: rt, Target: "y"}
		_, err := Fit([]*types.RegressionSpec{spec}, nil)
		if err == nil {
			t.Errorf("Fit(%s) returned nil error; want PROCESSING_REGRESSION_NOT_IMPLEMENTED", rt)
			continue
		}
		if !errors.HasCode(err, errors.PROCESSING_REGRESSION_NOT_IMPLEMENTED) {
			t.Errorf("Fit(%s) error code = %v, want PROCESSING_REGRESSION_NOT_IMPLEMENTED", rt, err)
		}
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
