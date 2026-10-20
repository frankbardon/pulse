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
