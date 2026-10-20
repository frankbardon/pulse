package descriptor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// TestRegressionCapabilities_ParamsAreSlotKeys (#371): a built-in
// regression reads no params object — every knob is a typed
// types.RegressionSpec field — so every Param its manifest entry
// declares must be marked Slot (descriptor.Param.Slot: "every
// RegressionSpec knob") and must name a top-level RegressionSpec JSON
// key. A non-slot param, or a key nested under an object (bounds'
// nonnegative, covariance's type), cannot sneak into the list.
func TestRegressionCapabilities_ParamsAreSlotKeys(t *testing.T) {
	spec := map[string]bool{}
	rt := reflect.TypeOf(types.RegressionSpec{})
	for i := 0; i < rt.NumField(); i++ {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if key != "" && key != "-" && key != "params" {
			spec[key] = true
		}
	}
	caps := regressionCapabilities()
	if len(caps) != len(types.AllRegressionTypes()) {
		t.Fatalf("%d regression capabilities, %d registered types", len(caps), len(types.AllRegressionTypes()))
	}
	for _, c := range caps {
		if len(c.Params) == 0 {
			t.Errorf("%s declares no params", c.Name)
		}
		for _, p := range c.Params {
			if !p.Slot {
				t.Errorf("%s param %q lacks Slot: true (a RegressionSpec knob, never a params key)", c.Name, p.Name)
			}
			if !spec[p.Name] {
				t.Errorf("%s param %q is not a top-level types.RegressionSpec JSON key", c.Name, p.Name)
			}
		}
	}
}

// TestRegressionParamsAllowlist_FromManifest: the built-in regression
// params allowlist is the manifest Param list split by Slot — no
// params key, every declared knob listed as a slot key — so a slot key
// written under params is refused PULSE_REQUEST_UNKNOWN_FIELD.
func TestRegressionParamsAllowlist_FromManifest(t *testing.T) {
	allow := ParamsAllowlist(nil)
	for _, c := range regressionCapabilities() {
		d, ok := allow[paramread.Key{Category: paramread.Regression, Op: c.Name}]
		if !ok || !d.Declared {
			t.Fatalf("%s: no declared allowlist entry", c.Name)
		}
		if len(d.Keys) != 0 {
			t.Errorf("%s: params keys %v, want none (a built-in reads no params object)", c.Name, d.Keys)
		}
		if len(d.Slot) != len(c.Params) {
			t.Errorf("%s: %d slot keys %v, want the %d declared params", c.Name, len(d.Slot), d.Slot, len(c.Params))
		}
	}
}
