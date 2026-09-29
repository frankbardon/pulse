package types

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// PairwiseOverlayParams is the decoded OverlaySpec.Params shape for the
// OVERLAY_PAIRWISE_* family. Every field is optional; the zero value
// (no params) means "every pair-axis index vs every other, cell n
// unweighted, cell value as a 0..100 percentage". Axis (row vs column
// pairing) rides on OverlaySpec.Scope, not here.
type PairwiseOverlayParams struct {
	// PairAlongDim, when non-nil, restricts pair generation to pair-axis
	// indexes whose key tuples agree on every dim position EXCEPT this
	// one ("all dims agree except this one" buckets). nil = every pair.
	// Must be >= 0 and < the pair-axis dim count.
	PairAlongDim *int `json:"pair_along_dim,omitempty"`

	// NSource selects where the sample-size leg is read. One of the
	// PairwiseNSource* constants. Empty = cell_n_unweighted. Ignored by
	// the Welford-input kinds (welch / two-means read n from the triple).
	NSource string `json:"n_source,omitempty"`

	// NWithinDepth, with NSource=n_within or n_within_distinct, fixes the
	// first NWithinDepth+1 pair-axis dim positions in the denominator
	// (mirrors CrosstabSpec.NormalizeWithin). Must be >= 0.
	NWithinDepth int `json:"n_within_depth,omitempty"`

	// PSource selects how the proportion leg is derived for the
	// proportion-input kinds (prop-Z, probit-t). One of the
	// PairwisePSource* constants. Empty = cell_value_pct. Ignored by the
	// Welford-input kinds.
	PSource string `json:"p_source,omitempty"`
}

// Pairwise sample-size source modes (PairwiseOverlayParams.NSource).
const (
	PairwiseNSourceCellNUnweighted = "cell_n_unweighted"
	PairwiseNSourceCellValueWeight = "cell_value_weighted"
	PairwiseNSourceRowMarginN      = "row_margin_n"
	PairwiseNSourceColumnMarginN   = "column_margin_n"
	PairwiseNSourceNWithin         = "n_within"
	PairwiseNSourceCellWeightSum   = "cell_weight_sum"

	// PairwiseNSourceNWithinDistinct is n_within's distinct-KEY sibling:
	// the same fixed-prefix slab, accumulating the cell aggregator's
	// distinct-key cardinality instead of its record count. Admitted only
	// on an AGG_DISTINCT_SUM cell (read at "distinct_count") or an
	// AGG_DISTINCT_COUNT cell (read at "cardinality"); every other cell
	// aggregator is refused, because AGG_FREQUENCY and AGG_MODE spell a
	// DISTINCT-VALUE figure with the same "distinct_count" key and reading
	// it as a sample size is silently wrong.
	PairwiseNSourceNWithinDistinct = "n_within_distinct"
)

// PairwiseNSourceUsesWithinDepth reports whether s is one of the
// fixed-prefix slab modes that read NWithinDepth. Both the depth
// range guard and the slab accumulators key off this, so adding a
// third slab mode does not need the guard rewritten.
func PairwiseNSourceUsesWithinDepth(s string) bool {
	return s == PairwiseNSourceNWithin || s == PairwiseNSourceNWithinDistinct
}

// PairwiseNSourceSumsDistinctCells reports whether s accumulates a slab
// by SUMMING per-cell DISTINCT-KEY cardinalities.
//
// Deliberately NOT PairwiseNSourceUsesWithinDepth. Plain n_within sums
// per-cell RECORD counts, and record counts are additive under any
// grouper — a record that fans into two buckets is genuinely two
// contributions to the record total, so the sum is right. Distinct-key
// cardinalities are NOT additive: a key present in two cells of one
// slab is counted twice, and the summed n over-states the sample size.
// Only the distinct modes need the partition precondition, so gating on
// UsesWithinDepth would refuse n_within requests that are correct today.
func PairwiseNSourceSumsDistinctCells(s string) bool {
	return s == PairwiseNSourceNWithinDistinct
}

// PairwiseSlabPartitionViolation names the pair-axis dim that breaks a
// distinct-key slab's partition precondition: the offending dim index,
// the grouper type that fans out there, the field it groups, and which
// host axis it sits on ("row" / "column").
type PairwiseSlabPartitionViolation struct {
	DimIndex  int
	GroupType GroupType
	Field     string
	Axis      string
}

// CheckPairwiseSlabPartition reports the first pair-axis dim that breaks
// the distinct-key slab's partition precondition, or ok=false when the
// request is fine (which includes every non-distinct n_source).
//
// Summing per-cell distinct cardinalities equals the slab's true
// distinct count only when the slab's cells PARTITION the key set. The
// slab fixes the first NWithinDepth+1 pair-axis dims and sums across
// every dim after them, so a fan-out grouper at depth d > NWithinDepth
// can land one key in two summed cells.
//
// Two shapes are deliberately ACCEPTED:
//
//   - A fan-out grouper at depth d <= NWithinDepth. It sits inside the
//     FIXED prefix, so it multiplies slabs rather than cells: each slab
//     still partitions its own keys. This is the shape the distinct
//     mode exists for and refusing it would refuse the filed request.
//   - A fan-out grouper on the OPPOSITE axis, at any depth. The slab
//     never sums across the opposite axis — each opposite coordinate is
//     its own denominator.
//
// Extension groupers are NOT covered: GroupType.FansOut() only knows
// built-in constants and a registered extension grouper falls through
// to false. Closing that needs the extensions snapshot, not a
// name-prefix heuristic.
func CheckPairwiseSlabPartition(ct *CrosstabSpec, scope OverlayScope, params PairwiseOverlayParams) (PairwiseSlabPartitionViolation, bool) {
	var zero PairwiseSlabPartitionViolation
	if ct == nil || !PairwiseNSourceSumsDistinctCells(params.NSource) {
		return zero, false
	}
	var axisGroups []*Group
	var axis string
	switch scope {
	case OverlayScopeRow:
		axisGroups, axis = ct.Rows, "row"
	case OverlayScopeColumn:
		axisGroups, axis = ct.Columns, "column"
	default:
		// Neither pair axis — the scope gate refuses this separately.
		return zero, false
	}
	// The summed-across dims start one past the fixed prefix. A negative
	// NWithinDepth is refused by its own range guard; clamp so this gate
	// never reads a negative index out from under it.
	start := params.NWithinDepth + 1
	if start < 0 {
		start = 0
	}
	for d := start; d < len(axisGroups); d++ {
		g := axisGroups[d]
		if g == nil || !g.Type.FansOut() {
			continue
		}
		return PairwiseSlabPartitionViolation{
			DimIndex:  d,
			GroupType: g.Type,
			Field:     g.Field,
			Axis:      axis,
		}, true
	}
	return zero, false
}

// Message renders the refusal prose for the distinct-key slab
// partition gate. It lives here, not on either arm, because predict
// (descriptor.validateOverlayPairwise) and runtime
// (processing.applyOverlaysToResponse) must refuse the same request
// with the same words as well as the same code — two hand-written
// twins would drift.
func (v PairwiseSlabPartitionViolation) Message(kind OverlayKind, params PairwiseOverlayParams) string {
	return "overlay " + string(kind) + " n_source=" + params.NSource +
		" sums per-cell distinct counts across " + v.Axis + "-axis dim " + strconv.Itoa(v.DimIndex) +
		" (" + string(v.GroupType) + " on field " + v.Field + "), which fans one record into" +
		" more than one bucket: those cells do not partition the key set, so the summed n" +
		" over-states the sample size. Raise n_within_depth to >= " + strconv.Itoa(v.DimIndex) +
		" so the fan-out dim sits inside the fixed prefix, or use n_source=n_within (record" +
		" counts are additive)"
}

// Details builds the structured payload both arms emit. index is the
// Request.Overlays position of the offending spec.
func (v PairwiseSlabPartitionViolation) Details(kind OverlayKind, params PairwiseOverlayParams, index int) map[string]any {
	return map[string]any{
		"index":          index,
		"kind":           string(kind),
		"n_source":       params.NSource,
		"n_within_depth": params.NWithinDepth,
		"dim_index":      v.DimIndex,
		"group_type":     string(v.GroupType),
		"field":          v.Field,
		"axis":           v.Axis,
	}
}

// Pairwise proportion source modes (PairwiseOverlayParams.PSource).
const (
	PairwisePSourceCellValuePct = "cell_value_pct"
	PairwisePSourceCellValue    = "cell_value"
)

// ValidPairwiseNSource reports whether s names a supported NSource mode.
// Empty counts as valid (defaults to cell_n_unweighted).
func ValidPairwiseNSource(s string) bool {
	switch s {
	case "",
		PairwiseNSourceCellNUnweighted,
		PairwiseNSourceCellValueWeight,
		PairwiseNSourceRowMarginN,
		PairwiseNSourceColumnMarginN,
		PairwiseNSourceNWithin,
		PairwiseNSourceNWithinDistinct,
		PairwiseNSourceCellWeightSum:
		return true
	}
	return false
}

// ValidPairwisePSource reports whether s names a supported PSource mode.
// Empty counts as valid (defaults to cell_value_pct).
func ValidPairwisePSource(s string) bool {
	switch s {
	case "", PairwisePSourceCellValuePct, PairwisePSourceCellValue:
		return true
	}
	return false
}

// DecodePairwiseParams decodes a raw OverlaySpec.Params blob into a
// PairwiseOverlayParams. A nil / empty blob yields the zero value (all
// defaults). Malformed JSON returns an error so callers surface a clean
// predict-time / runtime diagnostic rather than panicking.
func DecodePairwiseParams(raw json.RawMessage) (PairwiseOverlayParams, error) {
	var p PairwiseOverlayParams
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("decode pairwise overlay params: %w", err)
	}
	return p, nil
}

// IsPairwiseOverlayKind reports whether kind is a member of the
// OVERLAY_PAIRWISE_* family.
func IsPairwiseOverlayKind(kind OverlayKind) bool {
	switch kind {
	case OverlayKindPairwisePropZ,
		OverlayKindPairwiseProbitT,
		OverlayKindPairwiseWelchT,
		OverlayKindPairwiseTwoMeansZ:
		return true
	}
	return false
}

// PairwiseKindUsesWelford reports whether kind reads the Welford triple
// {mean, variance, n} (welch / two-means) rather than a proportion + n.
func PairwiseKindUsesWelford(kind OverlayKind) bool {
	return kind == OverlayKindPairwiseWelchT || kind == OverlayKindPairwiseTwoMeansZ
}
