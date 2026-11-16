package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestOrder_HiddenCapabilityRefused: on an instance without
// capability:regression_order a set order is an unknown field at its
// regression (details.path), absent from the visible keys; enabled, it
// passes the gate.
func TestOrder_HiddenCapabilityRefused(t *testing.T) {
	t.Parallel()

	req := &types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Order: &types.RegressionOrder{Field: "t"}}}}
	hidden := scopedOnly(featProcess, "REG_OLS")
	err := SlotRefusal(req, hidden)
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || ce.Details["path"] != "regressions[0].order" {
		t.Fatalf("hidden: err = %v", err)
	}
	if slices.Contains(VisibleSlotKeys(req.Regressions[0], hidden), "order") {
		t.Fatal("hidden instance lists order as a valid key")
	}
	if err := SlotRefusal(req, scopedOnly(featProcess, "REG_OLS", featRegOrder)); err != nil {
		t.Fatalf("enabled: err = %v", err)
	}
}

// TestExtensionRegressionOrderRefusal: a declared requirement without a
// spec order is order_required; with an order, a nil hook, or a hook
// saying no it passes; a panic is PULSE_EXTENSION_SPEC_REFUSED; the hook
// judges a copy.
func TestExtensionRegressionOrderRefusal(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Type: "REG_ACME_X", Name: "m", Target: "y", Predictors: []string{"x"}}
	always := ExtensionRegression{RequiresOrder: func(s *types.RegressionSpec) bool { s.Name = "mutated"; return true }}
	got := ExtensionRegressionOrderRefusal(spec, always)
	if got == nil || got.Code != errors.PROCESSING_CONFIG || got.Details["reason"] != "order_required" || got.Details["name"] != "m" {
		t.Fatalf("required: %v", got)
	}
	if spec.Name != "m" {
		t.Errorf("the hook mutated the caller's spec: %q", spec.Name)
	}
	ordered := *spec
	ordered.Order = &types.RegressionOrder{Field: "t"}
	for name, c := range map[string]struct {
		spec *types.RegressionSpec
		decl ExtensionRegression
	}{
		"ordered":  {&ordered, always},
		"nil_hook": {spec, ExtensionRegression{}},
		"says_no":  {spec, ExtensionRegression{RequiresOrder: func(*types.RegressionSpec) bool { return false }}},
	} {
		if got := ExtensionRegressionOrderRefusal(c.spec, c.decl); got != nil {
			t.Errorf("%s: %v", name, got)
		}
	}
	boom := ExtensionRegression{RequiresOrder: func(*types.RegressionSpec) bool { panic("boom") }}
	got = ExtensionRegressionOrderRefusal(spec, boom)
	if got == nil || got.Code != errors.PULSE_EXTENSION_SPEC_REFUSED || got.Details["panic"] != "boom" || got.Details["slot"] != "regressions" {
		t.Fatalf("panic: %v", got)
	}
}
