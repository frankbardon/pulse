package types

import (
	"strings"
	"testing"
)

func intPtr(v int) *int { return &v }

// rowAxis builds one panel slot's row axis from a list of group types.
// Field names are distinct so a diagnostic naming the wrong dim is
// visible in the failure message rather than being masked.
func panelSlot(panelIdx, slotIdx int, label string, types_ ...GroupType) PanelSlabPartitionSlot {
	rows := make([]*Group, 0, len(types_))
	for i, t := range types_ {
		rows = append(rows, &Group{Type: t, Field: "dim" + string(rune('a'+i))})
	}
	return PanelSlabPartitionSlot{Rows: rows, PanelIndex: panelIdx, SlotIndex: slotIdx, Label: label}
}

// The gate's whole decision table. Each row is a (slot shape, params)
// pair and the expected refusal.
func TestCheckPanelSlabPartition(t *testing.T) {
	for _, tc := range []struct {
		name   string
		slots  []PanelSlabPartitionSlot
		params PanelOverlayParams
		bad    bool
		dim    int
		panel  int
	}{
		{
			// The omitted-depth form sums NOTHING — it reads the exact
			// per-slot row margin — so a fan-out row axis is harmless
			// and must not be refused. Gating it would refuse a request
			// exactly as correct as the legacy default it reproduces.
			name:   "omitted depth is never gated even under fan-out",
			slots:  []PanelSlabPartitionSlot{panelSlot(0, 0, "ref", GROUP_CATEGORY, GROUP_SET_PER_ELEMENT)},
			params: PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin},
			bad:    false,
		},
		{
			name:   "fan-out beyond the fixed prefix refuses",
			slots:  []PanelSlabPartitionSlot{panelSlot(0, 0, "ref", GROUP_CATEGORY, GROUP_SET_PER_ELEMENT)},
			params: PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(0)},
			bad:    true,
			dim:    1,
			panel:  0,
		},
		{
			// d <= NWithinDepth sits INSIDE the fixed prefix: it
			// multiplies slabs, not cells, and each slab still
			// partitions its own keys. Same rule the axis-pairing arm
			// applies.
			name:   "fan-out inside the fixed prefix is accepted",
			slots:  []PanelSlabPartitionSlot{panelSlot(0, 0, "ref", GROUP_SET_PER_ELEMENT, GROUP_CATEGORY)},
			params: PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(0)},
			bad:    false,
		},
		{
			name:   "fan-out exactly at the prefix boundary is accepted",
			slots:  []PanelSlabPartitionSlot{panelSlot(0, 0, "ref", GROUP_CATEGORY, GROUP_SET_PER_ELEMENT, GROUP_CATEGORY)},
			params: PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(1)},
			bad:    false,
		},
		{
			// A clean reference and a dirty TARGET. The offender is
			// found in PANEL order, and it refuses the whole spec.
			name: "one offending target slot refuses the spec",
			slots: []PanelSlabPartitionSlot{
				panelSlot(0, 0, "ref", GROUP_CATEGORY, GROUP_CATEGORY),
				panelSlot(1, 1, "t1", GROUP_CATEGORY, GROUP_CATEGORY),
				panelSlot(2, 2, "t2", GROUP_CATEGORY, GROUP_SET_PER_ELEMENT),
			},
			params: PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(0)},
			bad:    true,
			dim:    1,
			panel:  2,
		},
		{
			// A mode that does not consume the depth sums nothing.
			// (Reaching this gate with a depth set is impossible in
			// practice — the inert-param guard fires first — but the
			// gate must not assume its callers ordered themselves.)
			// The row axis carries the fan-out at dim 1, i.e. OUTSIDE
			// the depth-0 prefix — the exact shape that refuses under
			// a summing mode. Only the mode predicate stops it here,
			// so a regression that drops the predicate fails this row
			// rather than sailing past on a one-dim axis with nothing
			// to sum.
			name:   "non-summing mode is not gated",
			slots:  []PanelSlabPartitionSlot{panelSlot(0, 0, "ref", GROUP_CATEGORY, GROUP_SET_PER_ELEMENT)},
			params: PanelOverlayParams{NSource: PanelNSourceRowMarginValue, NWithinDepth: intPtr(0)},
			bad:    false,
		},
		{
			// A negative depth has its own refusal on both arms. This
			// gate must not index out from under it.
			name:   "negative depth clamps rather than panicking",
			slots:  []PanelSlabPartitionSlot{panelSlot(0, 0, "ref", GROUP_SET_PER_ELEMENT)},
			params: PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(-3)},
			bad:    true,
			dim:    0,
			panel:  0,
		},
		{
			name:   "no slots is not a violation",
			slots:  nil,
			params: PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(0)},
			bad:    false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, bad := CheckPanelSlabPartition(tc.slots, tc.params)
			if bad != tc.bad {
				t.Fatalf("CheckPanelSlabPartition bad = %v, want %v (violation %+v)", bad, tc.bad, v)
			}
			if !bad {
				return
			}
			if v.DimIndex != tc.dim {
				t.Errorf("DimIndex = %d, want %d", v.DimIndex, tc.dim)
			}
			if v.PanelIndex != tc.panel {
				t.Errorf("PanelIndex = %d, want %d", v.PanelIndex, tc.panel)
			}
			if v.GroupType != GROUP_SET_PER_ELEMENT {
				t.Errorf("GroupType = %q, want %q", v.GroupType, GROUP_SET_PER_ELEMENT)
			}
		})
	}
}

// A nil *Group in the row slice must be skipped, not dereferenced. The
// axis-pairing twin takes the same posture.
func TestCheckPanelSlabPartition_NilGroupSkipped(t *testing.T) {
	slots := []PanelSlabPartitionSlot{{
		Rows:  []*Group{{Type: GROUP_CATEGORY}, nil, {Type: GROUP_SET_PER_ELEMENT, Field: "brands"}},
		Label: "ref",
	}}
	v, bad := CheckPanelSlabPartition(slots,
		PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(0)})
	if !bad {
		t.Fatal("nil group swallowed the fan-out dim behind it")
	}
	if v.DimIndex != 2 {
		t.Errorf("DimIndex = %d, want 2", v.DimIndex)
	}
}

// Extension groupers reach the gate through the resolver, exactly as
// they do on the axis-pairing arm: built-in first, then the resolver,
// and a name known to NEITHER passes (it cannot execute, so it can
// produce no wrong number).
func TestCheckPanelSlabPartitionWith_ExtensionGrouper(t *testing.T) {
	const custom GroupType = "GROUP_CUSTOM_FANOUT"
	slots := []PanelSlabPartitionSlot{panelSlot(0, 0, "ref", GROUP_CATEGORY, custom)}
	params := PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(0)}

	if _, bad := CheckPanelSlabPartitionWith(slots, params, nil); bad {
		t.Error("nil resolver refused an unregistered grouper; it cannot execute, so it can produce no wrong n")
	}
	declaredFlat := func(GroupType) (bool, bool) { return false, true }
	if _, bad := CheckPanelSlabPartitionWith(slots, params, declaredFlat); bad {
		t.Error("a registration declaring FansOut=false was refused")
	}
	declaredFanOut := func(GroupType) (bool, bool) { return true, true }
	v, bad := CheckPanelSlabPartitionWith(slots, params, declaredFanOut)
	if !bad {
		t.Fatal("an extension grouper declaring FansOut=true was not refused")
	}
	if v.GroupType != custom {
		t.Errorf("GroupType = %q, want %q", v.GroupType, custom)
	}
}

// A built-in ALWAYS answers from its own constant — an embedder
// resolver cannot talk a built-in fan-out grouper into looking flat.
func TestCheckPanelSlabPartitionWith_BuiltinWinsOverResolver(t *testing.T) {
	slots := []PanelSlabPartitionSlot{panelSlot(0, 0, "ref", GROUP_CATEGORY, GROUP_SET_PER_ELEMENT)}
	params := PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(0)}
	liar := func(GroupType) (bool, bool) { return false, true }
	if _, bad := CheckPanelSlabPartitionWith(slots, params, liar); !bad {
		t.Fatal("an extension resolver overrode a built-in's FansOut()")
	}
}

// Message and Details are shared by both arms, so their content is
// pinned here rather than in either arm's test.
func TestPanelSlabPartitionViolation_MessageAndDetails(t *testing.T) {
	params := PanelOverlayParams{NSource: PanelNSourceRowMarginValueWithin, NWithinDepth: intPtr(0)}
	v := PanelSlabPartitionViolation{
		DimIndex: 1, GroupType: GROUP_SET_PER_ELEMENT, Field: "brands",
		PanelIndex: 2, SlotIndex: 5, SlotLabel: "wave3",
	}
	msg := v.Message(OverlayKindPropZPanel, params)
	for _, want := range []string{"wave3", "brands", string(GROUP_SET_PER_ELEMENT), "n_within_depth"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Message() omits %q: %s", want, msg)
		}
	}
	d := v.Details(OverlayKindPropZPanel, params, 3)
	for k, want := range map[string]any{
		"index": 3, "dim_index": 1, "panel_index": 2, "slot_index": 5,
		"slot_label": "wave3", "axis": "row", "n_within_depth": 0,
		"n_source": PanelNSourceRowMarginValueWithin,
	} {
		if d[k] != want {
			t.Errorf("Details()[%q] = %v, want %v", k, d[k], want)
		}
	}
}
