package descriptor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestRegressionSlotRules_KeysAreSpecKeys: every key the
// declare-or-refuse table names is a types.RegressionSpec JSON key,
// classified exactly once (a rule key, or a field class, never both),
// and every rule leads with its own slot key and carries a refusal.
func TestRegressionSlotRules_KeysAreSpecKeys(t *testing.T) {
	t.Parallel()

	spec := map[string]bool{}
	rt := reflect.TypeOf(types.RegressionSpec{})
	for i := 0; i < rt.NumField(); i++ {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		spec[key] = true
	}
	seen := map[string]string{}
	for key := range regressionFieldClasses {
		if !spec[key] {
			t.Errorf("field class names %q, not a RegressionSpec key", key)
		}
		seen[key] = "class"
	}
	for _, r := range regressionSlotRules {
		if len(r.keys) == 0 || r.keys[0] != r.slot {
			t.Errorf("rule %q: keys %v must lead with the slot", r.slot, r.keys)
		}
		if r.refusal == nil || r.set == nil {
			t.Errorf("rule %q lacks set / refusal", r.slot)
		}
		for _, k := range r.keys {
			if !spec[k] {
				t.Errorf("rule %q names %q, not a RegressionSpec key", r.slot, k)
			}
			if prev, dup := seen[k]; dup {
				t.Errorf("key %q classified twice (%s and rule %q)", k, prev, r.slot)
			}
			seen[k] = "rule " + r.slot
		}
	}
}

// TestExtensionRegressionSlotRefusal_DeclarationFlips: each declarable
// slot is refused undeclared and passes declared; covariance follows
// its type list.
func TestExtensionRegressionSlotRefusal_DeclarationFlips(t *testing.T) {
	t.Parallel()

	base := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: "REG_ACME_X", Target: "y", Predictors: []string{"x"}}
		mut(s)
		return s
	}
	cases := []struct {
		slot string
		spec *types.RegressionSpec
		decl ExtensionRegression
	}{
		{"penalty", base(func(s *types.RegressionSpec) { s.Alpha = 1 }), ExtensionRegression{Penalty: true}},
		{"vcov", base(func(s *types.RegressionSpec) { s.Vcov = true }), ExtensionRegression{Vcov: true}},
		{"save", base(func(s *types.RegressionSpec) { s.Save = "m.pulse-model.json" }), ExtensionRegression{Scorer: true}},
		{"covariance", base(func(s *types.RegressionSpec) {
			s.Covariance = &types.RegressionCovariance{Type: types.CovarianceClassical}
		}),
			ExtensionRegression{CovarianceTypes: []types.CovarianceType{types.CovarianceClassical}}},
	}
	for _, c := range cases {
		got := ExtensionRegressionSlotRefusal(c.spec, ExtensionRegression{})
		if got == nil || got.Details["slot"] != c.slot {
			t.Errorf("%s undeclared: %v, want refusal at slot %s", c.slot, got, c.slot)
		}
		if got := ExtensionRegressionSlotRefusal(c.spec, c.decl); got != nil {
			t.Errorf("%s declared: refused %v", c.slot, got)
		}
	}
	if got := ExtensionRegressionSlotRefusal(base(func(*types.RegressionSpec) {}), ExtensionRegression{}); got != nil {
		t.Errorf("bare spec refused: %v", got)
	}
}

// TestExtensionRegressionSlotRefusal_NoResampleOptOut: resample and its
// knobs are an opt-out slot — honoured on a bare declaration, refused
// under slot resample (key naming a set knob) once NoResample is set;
// rng_seed stays accepted either way, and the manifest slot list drops
// resample exactly when the refusal fires.
func TestExtensionRegressionSlotRefusal_NoResampleOptOut(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"resample", "jackknife_groups", "jackknife_by", "bootstrap_iters"} {
		if class, slot, ok := RegressionFieldClassOf(key); !ok || class != RegressionFieldOptOut || slot != "resample" {
			t.Errorf("RegressionFieldClassOf(%q) = %q %q %v, want opt_out under resample", key, class, slot, ok)
		}
	}
	if class, _, _ := RegressionFieldClassOf("rng_seed"); class != RegressionFieldAccepted {
		t.Errorf("rng_seed class = %q, want accepted", class)
	}
	optOut := ExtensionRegression{NoResample: true}
	for _, c := range []struct {
		key  string
		spec *types.RegressionSpec
	}{
		{"resample", &types.RegressionSpec{Type: "REG_ACME_X", Resample: "bootstrap"}},
		{"jackknife_groups", &types.RegressionSpec{Type: "REG_ACME_X", JackknifeGroups: 4}},
		{"jackknife_by", &types.RegressionSpec{Type: "REG_ACME_X", JackknifeBy: "g"}},
		{"bootstrap_iters", &types.RegressionSpec{Type: "REG_ACME_X", BootstrapIters: 50}},
	} {
		if got := ExtensionRegressionSlotRefusal(c.spec, ExtensionRegression{}); got != nil {
			t.Errorf("%s on a bare declaration refused: %v", c.key, got)
		}
		got := ExtensionRegressionSlotRefusal(c.spec, optOut)
		if got == nil || got.Details["slot"] != "resample" {
			t.Fatalf("%s under NoResample: %v, want refusal at slot resample", c.key, got)
		}
		if k, has := got.Details["key"]; (c.key == "resample") == has || (has && k != c.key) {
			t.Errorf("%s under NoResample: details %v", c.key, got.Details)
		}
	}
	if got := ExtensionRegressionSlotRefusal(&types.RegressionSpec{Type: "REG_ACME_X", RNGSeed: 7}, optOut); got != nil {
		t.Errorf("rng_seed under NoResample refused: %v", got)
	}
	has := func(decl ExtensionRegression) bool {
		for _, p := range regressionSlotParamsFor(decl) {
			if p.Name == "resample" {
				return true
			}
		}
		return false
	}
	if !has(ExtensionRegression{}) || has(optOut) {
		t.Errorf("manifest resample slot: bare %v, NoResample %v; want true, false", has(ExtensionRegression{}), has(optOut))
	}
}
