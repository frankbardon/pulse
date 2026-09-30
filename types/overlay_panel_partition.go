package types

import "strconv"

// Partition precondition for the COMPOSE-host panel's within-prefix
// slab — the panel twin of CheckPairwiseSlabPartition
// (types/overlay_pairwise.go).
//
// WHY THE PANEL GATES A MODE THE AXIS-PAIRING FAMILY DOES NOT. Over
// there the split is narrow: PairwiseNSourceSumsDistinctCells gates
// only n_within_distinct, because plain n_within sums
// CrosstabComponents.CellCounts — RECORD COUNTS — and a record that
// fans into two buckets is genuinely two contributions to the record
// total, so the sum is right and gating it would refuse correct
// requests.
//
// The panel has no such guarantee to lean on. Its leg reads a MARGIN
// CELL's VALUE off MatrixPayload.RowMargins, which is whatever that
// slot's cell aggregator emitted: a record count under AGG_COUNT, a
// distinct-key cardinality under AGG_DISTINCT_COUNT, a weighted total
// under AGG_SUM, not a count at all under a percentage normalization.
// Summing those across a fan-out row axis double-counts whichever of
// them is not additive, and the panel cannot tell which it is holding.
// So every panel mode that consumes NWithinDepth is gated, and
// PanelNSourceUsesWithinDepth is the predicate — there is no narrower
// one to key off and inventing one would assert additivity the host
// cannot observe.
//
// Failure mode if this gate were absent: the denominator comes out
// LARGER than the truth, every pairwise p-value comes out
// conservative-to-meaningless, and nothing in the response says so.
// That is the same liberal-silent class the whole effort exists to
// remove.

// PanelSlabPartitionSlot is ONE resolved panel slot's row axis, in the
// form both gate arms can produce without importing each other:
// predict reads Request.Crosstab.Rows straight off the authored slot,
// runtime reads it off the same *Request the Compose orchestrator
// already carries.
//
// PanelIndex is the PANEL position (0 = reference, i = targets[i-1]),
// SlotIndex the authored ComposedRequest.Requests position, and Label
// the resolved slot label. All three ride the Details because a panel
// diagnostic that names only the panel index sends the caller hunting:
// panel 0 is the reference, which is rarely Compose slot 0.
type PanelSlabPartitionSlot struct {
	Rows       []*Group
	PanelIndex int
	SlotIndex  int
	Label      string
}

// PanelSlabPartitionViolation names the slot and the row-axis dim that
// break a panel prefix slab's partition precondition.
type PanelSlabPartitionViolation struct {
	DimIndex   int
	GroupType  GroupType
	Field      string
	PanelIndex int
	SlotIndex  int
	SlotLabel  string
}

// CheckPanelSlabPartition is CheckPanelSlabPartitionWith with no
// extension resolver — the built-in-only entry point, kept for callers
// with no registry in reach.
func CheckPanelSlabPartition(slots []PanelSlabPartitionSlot, params PanelOverlayParams) (PanelSlabPartitionViolation, bool) {
	return CheckPanelSlabPartitionWith(slots, params, nil)
}

// CheckPanelSlabPartitionWith reports the first panel slot and row-axis
// dim that break the within-prefix slab's partition precondition, or
// ok=false when the spec is fine.
//
// Three shapes are deliberately NOT gated:
//
//   - An OMITTED NWithinDepth. That form reads the EXACT per-slot row
//     margin and sums nothing at all, so no key can land in two summed
//     buckets however the row axis fans out. Gating it would refuse a
//     request that is exactly as correct as the legacy default it is
//     byte-identical to.
//   - A mode that does not consume the depth
//     (PanelNSourceUsesWithinDepth is false). Nothing sums.
//   - A fan-out grouper at depth d <= NWithinDepth. It sits inside the
//     FIXED prefix, so it multiplies SLABS rather than cells and each
//     slab still partitions its own keys. Same rule the axis-pairing
//     arm applies, and it is the shape the within-prefix mode exists
//     to serve.
//
// A negative depth is refused by its own range guard on both arms; the
// start index is clamped so this gate never reads a negative index out
// from under it.
//
// Slots are walked in PANEL order and the FIRST offender wins. One
// offending slot refuses the WHOLE spec — it is deliberately not a
// per-slot drop, because dropping a slot changes M, and M sets the
// length and the pair ordering of every cell's flattened
// upper-triangular vector: the caller would get a shorter vector with
// no way to tell which slot left. This is the same call the runtime
// depth range guard already makes.
func CheckPanelSlabPartitionWith(slots []PanelSlabPartitionSlot, params PanelOverlayParams, resolveExt ExtensionGroupFanOutFunc) (PanelSlabPartitionViolation, bool) {
	var zero PanelSlabPartitionViolation
	if params.NWithinDepth == nil || !PanelNSourceUsesWithinDepth(params.NSource) {
		return zero, false
	}
	start := *params.NWithinDepth + 1
	if start < 0 {
		start = 0
	}
	for _, slot := range slots {
		for d := start; d < len(slot.Rows); d++ {
			g := slot.Rows[d]
			if g == nil || !groupFansOut(g.Type, resolveExt) {
				continue
			}
			return PanelSlabPartitionViolation{
				DimIndex:   d,
				GroupType:  g.Type,
				Field:      g.Field,
				PanelIndex: slot.PanelIndex,
				SlotIndex:  slot.SlotIndex,
				SlotLabel:  slot.Label,
			}, true
		}
	}
	return zero, false
}

// Message renders the refusal prose. It lives here, not on either arm,
// because predict (descriptor.validateOverlayPanel) and runtime
// (processing.checkPanelSlabPartition) must refuse the same spec with
// the same words as well as the same code.
func (v PanelSlabPartitionViolation) Message(kind OverlayKind, params PanelOverlayParams) string {
	depth := 0
	if params.NWithinDepth != nil {
		depth = *params.NWithinDepth
	}
	return "overlay " + string(kind) + " n_source=" + params.NSource +
		" sums slot " + v.SlotLabel + "'s row margins across row-axis dim " + strconv.Itoa(v.DimIndex) +
		" (" + string(v.GroupType) + " on field " + v.Field + "), which fans one record into" +
		" more than one row: those rows do not partition the key set, so the summed n" +
		" over-states the sample size. Raise n_within_depth to >= " + strconv.Itoa(v.DimIndex) +
		" so the fan-out dim sits inside the fixed prefix, or omit n_within_depth entirely" +
		" (the exact per-slot row margin sums nothing). Current n_within_depth is " +
		strconv.Itoa(depth)
}

// Details builds the structured payload both arms emit. index is the
// ComposedRequest.Overlays position of the offending spec.
//
// panel_index AND slot_index both ride it, per the panel's standing
// rule: panel 0 is the reference, not Compose slot 0, so a diagnostic
// carrying only the panel index cannot be resolved against the
// caller's own request.
func (v PanelSlabPartitionViolation) Details(kind OverlayKind, params PanelOverlayParams, index int) map[string]any {
	out := map[string]any{
		"index":       index,
		"kind":        string(kind),
		"n_source":    params.NSource,
		"dim_index":   v.DimIndex,
		"group_type":  string(v.GroupType),
		"field":       v.Field,
		"axis":        "row",
		"panel_index": v.PanelIndex,
		"slot_index":  v.SlotIndex,
		"slot_label":  v.SlotLabel,
	}
	if params.NWithinDepth != nil {
		out["n_within_depth"] = *params.NWithinDepth
	}
	return out
}
