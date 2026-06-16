package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/types"
)

// Crosstab normalize-level / normalize-within key-prefix helpers shared
// between the buffered crosstab orchestrator (`processing/crosstab.go`)
// and the overlay handlers (`processing/overlay.go`). Per PRD § 4.C
// FR-C3 ("Reuse existing helpers; do not duplicate math") the overlay
// Level/Within plumbing reuses these pure functions rather than
// duplicating the partial-depth / cross-axis-partition math the
// crosstab `normalize_level` / `normalize_within` paths run.
//
// Helpers in this file are intentionally side-effect-free and consume
// only the AxisKey / depth / axis-length surface — they never touch
// raw records, never touch the schema, and never reach back into the
// orchestrator state. The buffered crosstab path keeps owning its
// PartitionByAxis-based recompute (which is the only correct path for
// recompute-class aggregators like AGG_MEDIAN); the overlay handlers
// reuse this surface to derive prefix keys from the host MatrixPayload's
// leaf RowKeys / ColumnKeys so per-row / per-column denominators land
// on the same partial-depth grouping the crosstab `normalize_level`
// gate would have used.
//
// Structural invariants:
//
//   - No fmt.Sprintf in any JSON-bearing path. Helpers are pure index
//     math; no formatting happens here.
//   - No imports of `service/` or `descriptor/`. Helpers live next to
//     the buffered crosstab orchestrator and overlay runtime.

// OverlayLevelEnabled reports whether a non-zero Level / Within slot
// on an OverlaySpec engages the prefix-axis denominator path. Mirrors
// the "zero-is-off" sentinel the OverlaySpec.Level / Within JSON tags
// already encode (`omitempty` omits the zero value on the wire). Used
// by the share / index / delta / zscore handler dispatch to skip the
// per-cell prefix-bucket precompute when the caller is on the legacy
// path — preserves the E1 / E2-S1..S9 overlay-handler byte-identity
// guarantee (story E2-S11 acceptance criterion: "Default Level=0,
// Within=0 MUST be byte-equivalent to the no-Level/no-Within path").
//
// Returns true when either slot is positive (non-zero). Negative slots
// (predict gate would reject; runtime gate would also reject) are
// treated as "not enabled" so the runtime handler fails closed on the
// gate before reaching the prefix-bucket code path.
func OverlayLevelEnabled(spec *types.OverlaySpec) bool {
	if spec == nil {
		return false
	}
	return spec.Level > 0 || spec.Within > 0
}

// SameAxisPrefixDepth returns the depth at which the SAME axis (the
// one the overlay is centerpoint-locked to) should be truncated when
// the spec's Level slot engages the prefix-denominator path. Counts
// from the leaf — `Level=1` truncates to the parent prefix (drops the
// last grouper); `Level=axisDepth-1` truncates to the root-most prefix
// (keeps only the first grouper). The returned depth is the prefix
// LENGTH (count of groupers retained), NOT a zero-based depth index —
// callers pass this directly to AxisKeyPrefix's `depth` argument as
// `length-1`.
//
// Returns axisDepth (= no truncation, full leaf) when Level <= 0, so
// the zero-default code path produces leaf-margin denominators byte-
// identical to the pre-S11 handler output.
//
// Out-of-range (Level >= axisDepth) is caught by the runtime
// validateOverlayLevelWithinRuntime gate before this function is
// reached; the helper clamps defensively to the [1, axisDepth] range
// to keep a misconfigured caller out of a panic path.
func SameAxisPrefixDepth(axisDepth, level int) int {
	if axisDepth <= 0 {
		return 0
	}
	if level <= 0 {
		return axisDepth // no truncation
	}
	if level >= axisDepth {
		// Out of range; clamp to root-most retained prefix (single
		// grouper). The runtime gate already rejected this case;
		// defensive clamp here keeps the helper panic-free for fuzz
		// callers and tests.
		return 1
	}
	return axisDepth - level
}

// OppositeAxisPrefixDepth returns the depth at which the OPPOSITE axis
// (the cross-axis the overlay does NOT centerpoint-lock to) should be
// fixed when the spec's Within slot engages the cross-axis-partition
// path. Mirrors SameAxisPrefixDepth's counting model — `Within=1` fixes
// the parent-prefix of the opposite axis (drops the last grouper);
// `Within=oppositeDepth-1` fixes the root-most prefix.
//
// Returns 0 (= no cross-axis fixing) when Within <= 0, so the zero-
// default code path produces leaf-margin denominators byte-identical
// to the pre-S11 handler output (the SHARE_OF_ROW denominator stays
// "row margin sums across all columns").
//
// The non-zero return is a prefix LENGTH (count of opposite-axis
// groupers retained), NOT a zero-based depth index — callers pass
// `length-1` to AxisKeyPrefix.
//
// Out-of-range (Within >= oppositeDepth) is caught by the runtime
// validateOverlayLevelWithinRuntime gate; defensive clamp here keeps
// the helper panic-free.
func OppositeAxisPrefixDepth(oppositeDepth, within int) int {
	if oppositeDepth <= 0 || within <= 0 {
		return 0
	}
	if within >= oppositeDepth {
		return 1
	}
	return oppositeDepth - within
}

// EffectiveAxisDepth clamps a caller-supplied depth (typically
// `OverlaySpec.Level`) into the valid `[0, axisDepth)` range and
// returns the effective leaf-or-prefix index the handler should use
// to truncate composite axis keys. Mirrors the CrosstabSpec
// NormalizeLevelOrLeaf clamp semantics (types/crosstab.go) so an
// overlay with Level=L and a crosstab with normalize_level=L compute
// against the same partial-axis depth.
//
// Behaviour summary:
//
//   - axisDepth <= 0 returns 0 (degenerate axis — no levels available;
//     the caller must guard against this case independently).
//   - level < 0 returns the leaf depth (axisDepth - 1). The on-wire
//     omitempty zero-default makes Level=0 the "leaf" sentinel from
//     the caller's perspective so this branch is the canonical
//     "default to leaf" path.
//   - level >= axisDepth is the predict-time PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
//     case — the runtime entry still gracefully clamps to the leaf
//     index so a misconfigured caller that survives predict surfaces
//     a degenerate-but-shaped overlay rather than a panic. The
//     descriptor.ValidateOverlays predict gate is the authoritative
//     rejection surface.
//
// The leaf depth is `axisDepth - 1` (zero-based level index). For a
// 2-deep row axis (`Rows=[brand, region]`, axisDepth=2) the valid
// levels are {0, 1}; Level=0 truncates to the `brand` prefix and
// Level=1 hits the leaf `(brand, region)` tuple.
func EffectiveAxisDepth(axisDepth, level int) int {
	if axisDepth <= 0 {
		return 0
	}
	leaf := axisDepth - 1
	if level < 0 || level >= axisDepth {
		return leaf
	}
	return level
}

// AxisKeyPrefix returns the first `depth+1` elements of an AxisKey
// joined as a composite key string using the canonical
// crosstabAxisKeySep separator. Mirrors the truncateCompositeKey shape
// the buffered crosstab orchestrator uses to address partial-margin
// buckets at depth `depth`.
//
// `depth` is the level index (0-based) — the same value
// EffectiveAxisDepth returns. A depth >= len(key)-1 returns the full
// leaf composite key (no truncation happens). A negative depth treats
// the key as a single full-leaf composite (defensive guard so a
// pre-clamp value never produces a panic; callers should always run
// EffectiveAxisDepth first).
//
// Used by the overlay handler dispatch to map leaf RowKeys[i] /
// ColumnKeys[j] tuples to the partial-prefix bucket the cell belongs
// to, so per-row / per-column denominators land on the same partial
// grouping the buffered crosstab `normalize_level` / `normalize_within`
// path would have used.
//
// The composite key separator is internal to the processing package
// (crosstabAxisKeySep). Callers should not parse the returned string —
// use it only as a map key to compare prefix-bucket membership.
func AxisKeyPrefix(key types.AxisKey, depth int) string {
	if len(key) == 0 {
		return ""
	}
	if depth < 0 {
		depth = len(key) - 1
	}
	last := depth + 1
	if last > len(key) {
		last = len(key)
	}
	parts := make([]string, 0, last)
	for i := 0; i < last; i++ {
		parts = append(parts, axisKeyElementString(key[i]))
	}
	return CompositeAxisKey(parts)
}

// axisKeyElementString renders one AxisKey element as a string in the
// same shape the buffered crosstab path produces. AxisKey carries
// `any` entries (numeric, categorical, date) so the renderer needs to
// handle every shape Grouper.Group emits.
//
// Today every grouper emits string keys (see Grouper.Group's contract
// at processing/grouper.go), but AxisKey deliberately carries `any` so
// numeric bin labels can stay numeric on the wire when the renderer
// formats them. The helper defers to fmt-free string rendering: a
// string element passes through; everything else falls back to the
// canonical Go default-format path via the fmtAny helper below. The
// crosstabAxisKeySep \x00 byte never appears in any grouper-emitted
// key (groupers emit stringified numerics, dictionary strings, or
// formatted date strings), so the rendered prefix is unambiguous.
//
// Used by AxisKeyPrefix above; not exported because the rendering
// shape is internal to the prefix-bucket lookup contract.
func axisKeyElementString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// overlayDenominatorAxis selects which cell-sum direction the
// prefix-denominator helper folds across. Used by buildOverlayDenominators
// so handlers can request the SHARE_OF_ROW direction (fold across
// columns within each row-prefix bucket) vs SHARE_OF_COL (fold across
// rows within each col-prefix bucket) vs SHARE_OF_TOTAL (fold across
// every cell in the matrix).
type overlayDenominatorAxis int

const (
	// overlayDenominatorAxisRow folds the per-cell denominator across
	// columns within each (rowPrefix, colPrefix) bucket. Used by
	// SHARE_OF_ROW + INDEX_VS_MARGIN(row) + DELTA_VS_MARGIN(row) +
	// ZSCORE_VS_MARGIN(row).
	overlayDenominatorAxisRow overlayDenominatorAxis = iota
	// overlayDenominatorAxisColumn folds the per-cell denominator across
	// rows within each (rowPrefix, colPrefix) bucket. Used by SHARE_OF_COL
	// + INDEX_VS_MARGIN(column) + DELTA_VS_MARGIN(column) +
	// ZSCORE_VS_MARGIN(column).
	overlayDenominatorAxisColumn
	// overlayDenominatorAxisGrand folds the per-cell denominator across
	// every present cell in the matrix; the (rowPrefix, colPrefix)
	// bucket discrimination is inert because the bucket is the entire
	// matrix. Used by SHARE_OF_TOTAL + INDEX_VS_MARGIN(grand) +
	// DELTA_VS_MARGIN(grand) + ZSCORE_VS_MARGIN(grand).
	overlayDenominatorAxisGrand
)

// overlayCellLocator addresses one cell on the host crosstab by its
// (rowIdx, colIdx) integer pair. Mirrors the integer-pair signature
// every overlay handler already uses to address host cells.
type overlayCellLocator struct {
	rowIdx int
	colIdx int
}

// overlayPrefixBucketKey identifies one (rowPrefix, colPrefix) bucket
// — the partial-margin denominator slot a cell belongs to when the
// overlay spec engages the Level / Within prefix-axis denominator
// path. Cells sharing the same key share a denominator.
type overlayPrefixBucketKey struct {
	row string
	col string
}
