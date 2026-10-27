package pulse_test

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/limits"
)

func TestRakeMissingPolicies_OrderAndFresh(t *testing.T) {
	t.Parallel()

	got := pulse.RakeMissingPolicies()
	if !slices.Equal(got, []string{pulse.RakeMissingRefuse, pulse.RakeMissingBase}) || got[0] != "refuse" {
		t.Fatalf("RakeMissingPolicies() = %v", got)
	}
	got[0] = "mutated"
	if pulse.RakeMissingPolicies()[0] != pulse.RakeMissingRefuse {
		t.Fatal("RakeMissingPolicies shares its backing array")
	}
}

// TestLimitNames_EveryNameParses: the facade list is the limit table in
// declaration order, and every name resolves through the parser the
// --limit flag and the feature profile use.
func TestLimitNames_EveryNameParses(t *testing.T) {
	t.Parallel()

	got := pulse.LimitNames()
	internal := limits.Names()
	if len(got) != len(internal) || len(got) == 0 {
		t.Fatalf("LimitNames() = %v, limits.Names() = %v", got, internal)
	}
	for i, n := range got {
		if n != string(internal[i]) {
			t.Errorf("LimitNames()[%d] = %q, want %q", i, n, internal[i])
		}
		if _, err := limits.ParseName(n); err != nil {
			t.Errorf("ParseName(%q): %v", n, err)
		}
	}
	got[0] = "mutated"
	if pulse.LimitNames()[0] != string(internal[0]) {
		t.Fatal("LimitNames shares its backing array")
	}
}

// TestWidenSetFieldTargets_SetRungsAboveTheNarrowest: every target is a
// set type name WidenSetField parses, strictly widening, and the
// narrowest rung (never a valid target) is absent.
func TestWidenSetFieldTargets_SetRungsAboveTheNarrowest(t *testing.T) {
	t.Parallel()

	got := pulse.WidenSetFieldTargets()
	if !slices.Equal(got, []string{"set_u16", "set_u32", "set_u64", "set_u128", "set_u256"}) {
		t.Fatalf("WidenSetFieldTargets() = %v", got)
	}
	prev := encoding.FieldTypeSetU8.MaxSetEntries()
	for _, name := range got {
		ft, ok := encoding.ParseFieldType(name)
		if !ok || !ft.IsSet() || ft.MaxSetEntries() <= prev {
			t.Errorf("%q: parsed=%v set=%v", name, ok, ft.IsSet())
		}
		prev = ft.MaxSetEntries()
	}
	got[0] = "mutated"
	if pulse.WidenSetFieldTargets()[0] != "set_u16" {
		t.Fatal("WidenSetFieldTargets shares its backing array")
	}
}
