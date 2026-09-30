package types

import "testing"

// Unit coverage for the extension-grouper half of the distinct-key
// slab partition gate: ResolveBuiltinGroupFanOut's three-way answer
// and the resolution order CheckPairwiseSlabPartitionWith imposes on
// top of it.
//
// Both gate arms delegate here — descriptor/ adapts the extensions
// snapshot, processing/ adapts the live registry — so the order lives
// in ONE place and the two cannot resolve the same name differently.

// TestResolveBuiltinGroupFanOut_ThreeWay pins the distinction
// GroupType.FansOut() cannot make on its own: a built-in that does not
// fan out and a name nobody registered both read false there, and only
// the second may fall through to an embedder registration.
func TestResolveBuiltinGroupFanOut_ThreeWay(t *testing.T) {
	cases := []struct {
		name      string
		t         GroupType
		wantFan   bool
		wantKnown bool
	}{
		{"built-in fan-out", GROUP_SET_PER_ELEMENT, true, true},
		{"built-in single-key", GROUP_CATEGORY, false, true},
		{"built-in single-key set", GROUP_SET_VALUE, false, true},
		{"extension name", GroupType("GROUP_ACME_PANEL_X"), false, false},
		{"empty name", GroupType(""), false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fan, known := ResolveBuiltinGroupFanOut(tc.t)
			if fan != tc.wantFan || known != tc.wantKnown {
				t.Fatalf("ResolveBuiltinGroupFanOut(%q) = (%v, %v), want (%v, %v)",
					tc.t, fan, known, tc.wantFan, tc.wantKnown)
			}
		})
	}
}

// TestResolveBuiltinGroupFanOut_CoversEveryBuiltin keeps the membership
// set honest: every AllGroupTypes() member must be known, and its
// answer must be exactly GroupType.FansOut(). A built-in that fell out
// of the set would silently become "resolvable by extension only".
func TestResolveBuiltinGroupFanOut_CoversEveryBuiltin(t *testing.T) {
	for _, g := range AllGroupTypes() {
		fan, known := ResolveBuiltinGroupFanOut(g)
		if !known {
			t.Errorf("%s is in AllGroupTypes() but ResolveBuiltinGroupFanOut reports it unknown", g)
			continue
		}
		if fan != g.FansOut() {
			t.Errorf("%s: resolver says fansOut=%v, GroupType.FansOut() says %v", g, fan, g.FansOut())
		}
	}
}

// extGroup is a pair-axis dim naming an embedder-registered grouper.
func extGroup(name, field string) *Group {
	return &Group{Type: GroupType(name), Field: field}
}

// TestCheckPairwiseSlabPartitionWith_ExtensionResolver drives the
// resolution order over the offending shape — flat OUTER, custom
// INNER, n_within_depth=0, so the inner dim is summed across.
func TestCheckPairwiseSlabPartitionWith_ExtensionResolver(t *testing.T) {
	const custom = "GROUP_ACME_PANEL_X"
	rows := []*Group{{Type: GROUP_CATEGORY, Field: "segment"}, extGroup(custom, "panel")}
	cols := []*Group{{Type: GROUP_CATEGORY, Field: "wave"}}
	params := PairwiseOverlayParams{NSource: PairwiseNSourceNWithinDistinct, NWithinDepth: 0}

	resolver := func(fansOut, known bool) ExtensionGroupFanOutFunc {
		return func(GroupType) (bool, bool) { return fansOut, known }
	}

	cases := []struct {
		name    string
		resolve ExtensionGroupFanOutFunc
		wantBad bool
	}{
		// The hole E2-S3 closes: declared fan-out, so refused.
		{"registered fan-out", resolver(true, true), true},
		// Declared single-key: the cells partition, so it runs.
		{"registered single-key", resolver(false, true), false},
		// Registered nowhere. Passes deliberately — the grouper cannot
		// execute, so no wrong n can come of it, and the accurate
		// diagnostic is the unknown-operator one.
		{"unknown to the resolver", resolver(false, false), false},
		// A resolver that claims fan-out but reports the name unknown
		// must not refuse: known=false wins, so a sloppy adapter cannot
		// invent a refusal out of a zero value.
		{"fan-out but unknown", resolver(true, false), false},
		// No resolver at all — the built-in-only entry point's shape.
		{"nil resolver", nil, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			v, bad := CheckPairwiseSlabPartitionWith(pwPartCT(rows, cols), OverlayScopeRow, params, tc.resolve)
			if bad != tc.wantBad {
				t.Fatalf("bad = %v, want %v (violation %+v)", bad, tc.wantBad, v)
			}
			if !bad {
				return
			}
			if v.DimIndex != 1 || string(v.GroupType) != custom || v.Field != "panel" || v.Axis != "row" {
				t.Errorf("violation = %+v, want dim 1 / %s / panel / row", v, custom)
			}
		})
	}
}

// TestCheckPairwiseSlabPartitionWith_BuiltinWinsOverResolver pins the
// ORDER, not just the fallback. A resolver that answers every name
// must not be able to flip a built-in's verdict in either direction:
// GROUP_SET_PER_ELEMENT stays refused and GROUP_CATEGORY stays
// accepted regardless of what the extension half says. Without the
// built-in-first branch an over-broad adapter would silently retune
// the gate for the whole built-in catalog.
func TestCheckPairwiseSlabPartitionWith_BuiltinWinsOverResolver(t *testing.T) {
	params := PairwiseOverlayParams{NSource: PairwiseNSourceNWithinDistinct, NWithinDepth: 0}
	cols := []*Group{{Type: GROUP_CATEGORY, Field: "wave"}}

	answerAll := func(fansOut bool) ExtensionGroupFanOutFunc {
		return func(GroupType) (bool, bool) { return fansOut, true }
	}

	fanRows := []*Group{{Type: GROUP_CATEGORY, Field: "segment"}, {Type: GROUP_SET_PER_ELEMENT, Field: "brand"}}
	if _, bad := CheckPairwiseSlabPartitionWith(pwPartCT(fanRows, cols), OverlayScopeRow, params, answerAll(false)); !bad {
		t.Error("a resolver answering false suppressed the GROUP_SET_PER_ELEMENT refusal")
	}

	flatRows := []*Group{{Type: GROUP_CATEGORY, Field: "segment"}, {Type: GROUP_CATEGORY, Field: "brand"}}
	if v, bad := CheckPairwiseSlabPartitionWith(pwPartCT(flatRows, cols), OverlayScopeRow, params, answerAll(true)); bad {
		t.Errorf("a resolver answering true invented a refusal for a built-in single-key dim: %+v", v)
	}
}

// TestCheckPairwiseSlabPartitionWith_ExtensionInPrefixRuns keeps the
// accepted shapes accepted for extension groupers too: inside the
// fixed prefix it multiplies slabs, and on the opposite axis the slab
// never sums across it. Both would be false refusals.
func TestCheckPairwiseSlabPartitionWith_ExtensionInPrefixRuns(t *testing.T) {
	const custom = "GROUP_ACME_PANEL_X"
	fanOut := func(GroupType) (bool, bool) { return true, true }
	flat := &Group{Type: GROUP_CATEGORY, Field: "segment"}

	t.Run("inside the fixed prefix", func(t *testing.T) {
		rows := []*Group{extGroup(custom, "panel"), flat}
		params := PairwiseOverlayParams{NSource: PairwiseNSourceNWithinDistinct, NWithinDepth: 0}
		if v, bad := CheckPairwiseSlabPartitionWith(pwPartCT(rows, []*Group{flat}), OverlayScopeRow, params, fanOut); bad {
			t.Errorf("refused an in-prefix extension fan-out: %+v", v)
		}
	})

	t.Run("opposite axis", func(t *testing.T) {
		rows := []*Group{flat, flat}
		cols := []*Group{flat, extGroup(custom, "panel")}
		params := PairwiseOverlayParams{NSource: PairwiseNSourceNWithinDistinct, NWithinDepth: 0}
		if v, bad := CheckPairwiseSlabPartitionWith(pwPartCT(rows, cols), OverlayScopeRow, params, fanOut); bad {
			t.Errorf("refused an opposite-axis extension fan-out: %+v", v)
		}
	})

	t.Run("plain n_within is never gated", func(t *testing.T) {
		rows := []*Group{flat, extGroup(custom, "panel")}
		params := PairwiseOverlayParams{NSource: PairwiseNSourceNWithin, NWithinDepth: 0}
		if v, bad := CheckPairwiseSlabPartitionWith(pwPartCT(rows, []*Group{flat}), OverlayScopeRow, params, fanOut); bad {
			t.Errorf("gated n_within under an extension fan-out: %+v", v)
		}
	})
}
