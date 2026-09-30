package types

import "testing"

// The panel's DISTINCT-KEY within-prefix mode (E4-S3).
//
// Three properties live at this layer and nowhere else: the wire name
// must not collide with the axis-pairing family's spelling, the mode
// must JOIN the predicates that already carry the within-depth and
// components contracts, and joining PanelNSourceUsesWithinDepth must
// be sufficient for the E4-S2 slab gate to cover it with no further
// wiring.

// The whole reason the mode is not called `n_within_distinct`. The
// axis-pairing family owns that string and means something else by it:
// it sums per-CELL distinct cardinalities over a slab of the PAIR axis
// at ONE fixed opposite index, while the panel's leg reads a slot's
// ROW margins — all columns. The two differ by roughly the column
// count, silently.
func TestPanelNSourceRowMarginDistinctWithin_DoesNotCollideWithPairwise(t *testing.T) {
	if PanelNSourceRowMarginDistinctWithin == PairwiseNSourceNWithinDistinct {
		t.Fatalf("panel and pairwise distinct modes share the wire name %q;"+
			" they read different carriers over different geometries and must not collide",
			PanelNSourceRowMarginDistinctWithin)
	}
	if PanelNSourceRowMarginDistinctWithin != "row_margin_distinct_within" {
		t.Errorf("mode = %q, want row_margin_distinct_within", PanelNSourceRowMarginDistinctWithin)
	}
	// The crosstab family's spelling must stay UNKNOWN on the panel.
	// Admitting it as an alias would restore exactly the ambiguity the
	// separate name exists to destroy.
	if ValidPanelNSource(PairwiseNSourceNWithinDistinct) {
		t.Errorf("panel accepts the crosstab spelling %q; it must be refused like any unknown mode",
			PairwiseNSourceNWithinDistinct)
	}
	if !ValidPanelNSource(PanelNSourceRowMarginDistinctWithin) {
		t.Fatal("ValidPanelNSource rejects the mode it is supposed to accept")
	}
}

// The mode CONSUMES n_within_depth (so an explicit depth is not an
// inert param), READS components (so a components-disabled slot is
// refused rather than read as zero), and READS DISTINCT KEYS (so the
// cell-aggregator admission gate applies to it). Each predicate is a
// separate contract with a separate call site.
func TestPanelNSourceRowMarginDistinctWithin_JoinsThePredicates(t *testing.T) {
	m := PanelNSourceRowMarginDistinctWithin
	if !PanelNSourceUsesWithinDepth(m) {
		t.Error("PanelNSourceUsesWithinDepth is false; n_within_depth would be refused as inert and the slab gate would never fire")
	}
	if !PanelNSourceReadsComponents(m) {
		t.Error("PanelNSourceReadsComponents is false; a components-disabled slot would fall through to a zero rather than refusing")
	}
	if !PanelNSourceReadsDistinctKeys(m) {
		t.Error("PanelNSourceReadsDistinctKeys is false; the cell-aggregator admission gate would never run")
	}
	if PanelNSourceFallsBackToCellValue(m) {
		t.Error("the distinct leg must NOT inherit the legacy cell-value fallback")
	}

	// cell_n_unweighted reads components too, but the universal-floor
	// "n" means the same thing under every aggregator, so it is NOT
	// admission-gated. If this ever becomes true the admission gate
	// starts refusing the counted mode's perfectly sound hosts.
	if PanelNSourceReadsDistinctKeys(PanelNSourceCellNUnweighted) {
		t.Error("cell_n_unweighted must not be admission-gated; the universal floor n is aggregator-independent")
	}
	if PanelNSourceReadsDistinctKeys(PanelNSourceRowMarginValueWithin) {
		t.Error("row_margin_value_within reads a PAYLOAD value, not a distinct key; it must not be admission-gated")
	}
	if PanelNSourceReadsDistinctKeys("") || PanelNSourceReadsDistinctKeys(PanelNSourceRowMarginValue) {
		t.Error("the legacy default must never be admission-gated")
	}
}

// PanelNSourcesUsingWithinDepth feeds the "it applies to X only"
// refusal. It is derived from the enum through the predicate itself,
// so a mode added to one cannot go missing from the other.
func TestPanelNSourcesUsingWithinDepth_MatchesThePredicate(t *testing.T) {
	got := PanelNSourcesUsingWithinDepth()
	want := []string{PanelNSourceRowMarginValueWithin, PanelNSourceRowMarginDistinctWithin}
	if len(got) != len(want) {
		t.Fatalf("PanelNSourcesUsingWithinDepth() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, m := range PanelNSources() {
		inList := false
		for _, g := range got {
			if g == m {
				inList = true
			}
		}
		if inList != PanelNSourceUsesWithinDepth(m) {
			t.Errorf("mode %q: listed=%v but PanelNSourceUsesWithinDepth=%v",
				m, inList, PanelNSourceUsesWithinDepth(m))
		}
	}
}

// GATE INHERITANCE at the shared predicate. E4-S2 built the gate off
// PanelNSourceUsesWithinDepth deliberately — no narrower
// SumsDistinct-style panel predicate — so joining that predicate is
// the whole wiring. This asserts the inheritance rather than assuming
// it: the SAME fan-out axis that refuses the value leg must refuse the
// distinct leg, and the omitted-depth form of both must stay ungated.
func TestCheckPanelSlabPartition_InheritsForDistinctWithin(t *testing.T) {
	fanOut := []PanelSlabPartitionSlot{{
		Rows: []*Group{
			{Type: GROUP_CATEGORY, Field: "region"},
			{Type: GROUP_SET_PER_ELEMENT, Field: "brands"},
		},
		PanelIndex: 0, SlotIndex: 0, Label: "ref",
	}}
	depth0 := 0

	v, bad := CheckPanelSlabPartition(fanOut, PanelOverlayParams{
		NSource:      PanelNSourceRowMarginDistinctWithin,
		NWithinDepth: &depth0,
	})
	if !bad {
		t.Fatal("the distinct within-prefix mode did not inherit the E4-S2 slab gate")
	}
	if v.DimIndex != 1 || v.GroupType != GROUP_SET_PER_ELEMENT {
		t.Errorf("violation = %+v, want dim 1 / GROUP_SET_PER_ELEMENT", v)
	}

	// Omitted depth sums nothing, so no key can land in two summed
	// buckets however the axis fans out.
	if _, bad := CheckPanelSlabPartition(fanOut, PanelOverlayParams{
		NSource: PanelNSourceRowMarginDistinctWithin,
	}); bad {
		t.Error("omitted depth was gated; it reads the exact per-slot margin and sums nothing")
	}

	// A fan-out INSIDE the fixed prefix multiplies slabs, not cells —
	// the shape the mode exists to serve.
	depth1 := 1
	if _, bad := CheckPanelSlabPartition(fanOut, PanelOverlayParams{
		NSource:      PanelNSourceRowMarginDistinctWithin,
		NWithinDepth: &depth1,
	}); bad {
		t.Error("a fan-out inside the fixed prefix was gated; each slab still partitions its own keys")
	}
}
