package descriptor

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Overlay validator — no-execute, header-and-schema-only validation
// for Request.Overlays specs. Walks every OverlaySpec and surfaces
// structural failures on the envelope alongside the aggregator / test
// / crosstab gates the rest of Predict runs.
//
// E1 scope (kind-catalog-v1 milestone S3):
//
//   - Unknown OverlayKind            → errors.PULSE_OVERLAY_KIND_UNKNOWN
//   - OVERLAY_INDEX_VS_MARGIN
//       * Ref family must be Margin and Margin.Axis must be a known
//         MarginAxis (row / column / grand).
//       * Host result must be MATRIX-shaped, i.e. Request.Crosstab is
//         non-nil. Without a crosstab there is no margin slot to
//         reference; mismatch fires
//         errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//       * Scope must be one of the supported scopes for the kind. E1
//         supports CELL only; everything else fires
//         errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED. Later epics widen the
//         set as ROW / COLUMN / TOTAL projections land.
//
// Structural invariants (CLAUDE.md "Predict / Inspect contracts" +
// "What NOT to Do"):
//
//   - This file MUST NOT import github.com/frankbardon/pulse/service
//     or github.com/frankbardon/pulse/processing. Predict is no-execute;
//     overlay catalog data lives in types/, capability lookups go
//     through types/ constants. TestPredictNoExecutionImports gates
//     the predict.go source list and CLAUDE.md "What NOT to Do" gates
//     the package as a whole.
//   - No fmt.Sprintf in any JSON-bearing path. Error messages are
//     built with string concatenation so descriptor envelope output
//     stays grep-clean against the structural defense ban.

// chiSqMatrixSupportedScopes is the E2-supported scope set for
// OVERLAY_CHISQ_MATRIX. The χ² independence test is a whole-matrix
// inferential overlay — Scope=MATRIX is the only sensible footprint
// and any other scope (CELL / ROW / COLUMN / TOTAL / GROUP) fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED.
var chiSqMatrixSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeMatrix: true,
}

// chiSqRowSupportedScopes is the E2-supported scope set for
// OVERLAY_CHISQ_ROW. The per-row χ² goodness-of-fit test is a ROW-
// scoped inferential overlay — Scope=ROW is the only sensible footprint
// and any other scope (CELL / COLUMN / MATRIX / TOTAL / GROUP) fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED.
var chiSqRowSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeRow: true,
}

// chiSqColSupportedScopes is the E2-supported scope set for
// OVERLAY_CHISQ_COL. The per-column χ² goodness-of-fit test is a COLUMN-
// scoped inferential overlay (mechanical column-axis twin of
// CHISQ_ROW) — Scope=COLUMN is the only sensible footprint and any other
// scope (CELL / ROW / MATRIX / TOTAL / GROUP) fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED.
var chiSqColSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeColumn: true,
}

// deltaVsMarginSupportedScopes is the E2-supported scope set for
// OVERLAY_DELTA_VS_MARGIN. DELTA_VS_MARGIN is a CELL-scoped overlay by
// construction — every cell receives an additive deviation against
// the matching margin slot. Like ZSCORE_VS_MARGIN the axis is not
// structurally locked: the validator accepts every known MarginAxis
// (row / column / grand) and the runtime handler dispatches the
// matching margin.
var deltaVsMarginSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
}

// deltaVsSiblingSupportedScopes is the E3-supported scope set for
// OVERLAY_DELTA_VS_SIBLING. The kind emits one entry per host group
// key — Scope=GROUP is the only sensible footprint and any other
// scope (CELL / ROW / COLUMN / MATRIX / TOTAL) fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED. Mirrors
// `indexVsSiblingSupportedScopes` — both sibling-reference kinds
// share the GROUP-only scope contract.
var deltaVsSiblingSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeGroup: true,
}

// fisherExactCellSupportedScopes is the E2-supported scope set for
// OVERLAY_FISHER_EXACT_CELL. The per-cell Fisher's exact 2×2 test is
// a CELL-scoped inferential overlay (canonical low-count χ² backstop;
// PRD § 4.C FR-C2) — every cell receives an exact two-sided p-value
// computed against a 2×2 contingency built from the cell + its row and
// column margins. Scope=CELL is the only sensible footprint and any
// other scope (ROW / COLUMN / MATRIX / TOTAL / GROUP) fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED.
var fisherExactCellSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
}

// indexVsMarginSupportedScopes is the E1-supported scope set for
// OVERLAY_INDEX_VS_MARGIN. Today only CELL ships; later epics widen
// the gate to ROW / COLUMN / TOTAL once the matching payload shapes
// are wired through processing.
var indexVsMarginSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
}

// indexVsPriorSupportedScopes is the E4-supported scope set for
// OVERLAY_INDEX_VS_PRIOR. The kind emits one entry per host group key
// (the ordered windowed series) — Scope=GROUP is the only sensible
// footprint and any other scope (CELL / ROW / COLUMN / MATRIX / TOTAL)
// fires PULSE_OVERLAY_SCOPE_UNSUPPORTED. Mirrors
// `indexVsTotalSupportedScopes`.
var indexVsPriorSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeGroup: true,
}

// indexVsSiblingSupportedScopes is the E3-supported scope set for
// OVERLAY_INDEX_VS_SIBLING. The kind emits one entry per host group
// key — Scope=GROUP is the only sensible footprint and any other
// scope (CELL / ROW / COLUMN / MATRIX / TOTAL) fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED. Mirrors
// `deltaVsSiblingSupportedScopes`.
var indexVsSiblingSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeGroup: true,
}

// indexVsTotalSupportedScopes is the E3-supported scope set for
// OVERLAY_INDEX_VS_TOTAL. INDEX_VS_TOTAL emits one per-group index
// score against the host series' grand total — Scope=GROUP is the
// only sensible footprint and any other scope (CELL / ROW / COLUMN /
// MATRIX / TOTAL) fires PULSE_OVERLAY_SCOPE_UNSUPPORTED.
var indexVsTotalSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeGroup: true,
}

// zscoreVsTotalSupportedScopes is the E3-supported scope set for
// OVERLAY_ZSCORE_VS_TOTAL. ZSCORE_VS_TOTAL emits one per-group
// standardized z-score against the host series' grand-total
// distribution — Scope=GROUP is the only sensible footprint and any
// other scope (CELL / ROW / COLUMN / MATRIX / TOTAL) fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED. Mirrors `indexVsTotalSupportedScopes`
// — the third streamable SERIES-host kind in the E3 grouped-Process
// subset shares the implicit-grand-total scope contract verbatim.
var zscoreVsTotalSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeGroup: true,
}

// shareOfRowSupportedScopes is the E2-supported scope set for
// OVERLAY_SHARE_OF_ROW. SHARE_OF_ROW is a CELL-scoped layer by
// construction — every cell divides by its row margin. ROW / COLUMN /
// TOTAL projections are not meaningful for this kind.
var shareOfRowSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
}

// shareOfColSupportedScopes is the E2-supported scope set for
// OVERLAY_SHARE_OF_COL. SHARE_OF_COL is a CELL-scoped layer by
// construction — every cell divides by its column margin. ROW /
// COLUMN / TOTAL projections are not meaningful for this kind.
var shareOfColSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
}

// shareOfTotalSupportedScopes is the supported scope set for the
// MATRIX-host dispatch of OVERLAY_SHARE_OF_TOTAL (E2-S3). The MATRIX
// dispatch is a CELL-scoped layer by construction — every cell divides
// by the grand total. ROW / COLUMN / TOTAL projections are not
// meaningful for the MATRIX dispatch.
//
// The SERIES-host dispatch (E3-S3) accepts GROUP scope and routes through
// `shareOfTotalSeriesSupportedScopes`. The host-shape pre-check in
// `validateOverlayShareOfTotal` selects between the two scope sets so
// each dispatch's rejection set is exhaustive for its own host.
var shareOfTotalSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
}

// shareOfTotalSeriesSupportedScopes is the supported scope set for the
// SERIES-host dispatch of OVERLAY_SHARE_OF_TOTAL (E3-S3). The SERIES
// dispatch emits one per-group share against the host series' grand
// total — Scope=GROUP is the only sensible footprint and any other
// scope (CELL / ROW / COLUMN / MATRIX / TOTAL) fires
// PULSE_OVERLAY_SCOPE_UNSUPPORTED. Mirrors `indexVsTotalSupportedScopes`.
var shareOfTotalSeriesSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeGroup: true,
}

// zscoreVsMarginSupportedScopes is the E2-supported scope set for
// OVERLAY_ZSCORE_VS_MARGIN. ZSCORE_VS_MARGIN is a CELL-scoped overlay
// by construction — every cell receives a deviation score against the
// matching margin slice's standard deviation. Unlike the SHARE_OF_*
// triad the axis is not structurally locked: the validator accepts
// every known MarginAxis (row / column / grand) and dispatches the
// matching slice at runtime.
var zscoreVsMarginSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
}

// validMarginAxes enumerates the on-wire MarginAxis values that an
// OverlayMarginRef may carry. Mirrors the constant block in
// types/overlay.go so the predict gate stays parity-true with the
// type-system source of truth.
var validMarginAxes = map[types.MarginAxis]bool{
	types.MarginAxisRow:    true,
	types.MarginAxisColumn: true,
	types.MarginAxisGrand:  true,
}

// ValidateOverlays walks every spec in req.Overlays and appends
// structural errors to the envelope. Exported so engines and tests
// can run the validator standalone; Predict wires it inline so
// overlay issues surface alongside crosstab / aggregator / test ones.
//
// No-op when req is nil or req.Overlays is empty. Schema is currently
// unused — every E1 rule is structural — but the signature accepts it
// so later kinds can validate against schema-derived state (e.g.
// referenced field types on sibling / baseline-index families) without
// re-opening every call site.
//
// Level / Within out-of-range gate (E2-S11): runs alongside the per-
// kind ref/scope checks via validateOverlayLevelWithinPredict. Mirrors
// the PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE shape on the
// crosstab path — out-of-range slots surface
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on the envelope with Details
// carrying the spec index, kind, level / within, and the axis depth.
// The runtime mirror lives at processing.validateOverlayLevelWithinRuntime
// so a programmatic Process caller that skipped predict still gets
// the same failure shape.
func ValidateOverlays(env *Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	if req == nil || len(req.Overlays) == 0 {
		return
	}
	_ = opts
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		validateOverlaySpec(env, req, spec, i)
		validateOverlayLevelWithinPredict(env, req, spec, i)
		validateOverlayBaselineIndexPredict(env, req, spec, schema, i)
	}
}

// validateOverlayBaselineIndexPredict is the no-execute predict mirror of
// `processing.ResolveBaselineIndex`. It walks the Ref.BaselineIndex arm
// and surfaces `PULSE_OVERLAY_REF_UNKNOWN` for negative or out-of-range
// `Position` values. Mirrors the runtime resolver's
// `{baseline_index, series_length}` Details map shape so MCP / CLI
// envelopes carry the same structured context the runtime would have
// emitted.
//
// Foundation gate (E4-S1) — the spec is intentionally tolerant of the
// existing E1..E3 overlay kinds because every shipping kind already
// rejects a populated `BaselineIndex` slot via its own per-kind validator
// (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE). When `BaselineIndex` is
// absent the gate is a no-op. When present:
//
//   - Negative `Position` always fires PULSE_OVERLAY_REF_UNKNOWN with
//     `series_length` derived from the predict-time schema upper bound
//     where computable, zero otherwise (the runtime resolver carries the
//     actual host length when the gate skipped predict).
//
//   - `Position >= predictedLength` fires PULSE_OVERLAY_REF_UNKNOWN ONLY
//     when the upper bound is derivable from the schema. Today the
//     only derivable case is a single-grouper SERIES host whose grouper
//     is `GROUP_CATEGORY` over a `categorical_u8 / u16 / u32` field —
//     the dict cardinality is the upper bound for the host's group-key
//     count. `GROUP_DATE` / `GROUP_RANGE` / `GROUP_ROUNDED` /
//     `GROUP_QUANTILE` upper bounds depend on bin width or runtime
//     content and cannot be predicted from the schema alone, so the
//     gate defers to runtime for those cases (the runtime resolver
//     still catches the out-of-range slot).
//
//   - Multi-grouper SERIES hosts: the cartesian product of dict
//     cardinalities is the upper bound, but only when every grouper
//     resolves to a categorical-dict-backed grouper. If any grouper is
//     non-categorical the predict gate defers to runtime (the cartesian
//     bound is not computable). When every grouper IS categorical the
//     gate multiplies dict counts; product is still an upper bound (the
//     actual host may emit fewer keys because of empty buckets, but the
//     resolver only fails when `Position` is strictly outside the
//     cartesian, which is always also outside the actual host length).
//
//   - Crosstab hosts: BaselineIndex.Position is the SERIES-host arm of
//     the OverlayBaselineIndexRef union — it has no meaning against a
//     MATRIX host (the Row + Column arms are the MATRIX-host slot, and
//     no shipping kind consumes those). The gate skips when
//     `req.Crosstab` is set so a future MATRIX-host kind landing on a
//     different Ref arm does not collide with the SERIES check.
func validateOverlayBaselineIndexPredict(env *Envelope, req *types.Request, spec *types.OverlaySpec, schema *encoding.Schema, index int) {
	if spec == nil || spec.Ref.BaselineIndex == nil {
		return
	}
	if req != nil && req.Crosstab != nil {
		// MATRIX host arm — the Position slot has no SERIES context. The
		// per-kind validator already rejects any populated BaselineIndex
		// pointer on the shipping MATRIX kinds; nothing further to gate
		// here.
		return
	}
	ref := spec.Ref.BaselineIndex
	// Compute the predicted series length upper bound. -1 means "not
	// derivable from schema; defer the range check to runtime".
	predictedLength := overlayBaselineIndexPredictedSeriesLength(req, schema)
	if ref.Position < 0 {
		env.AddError(string(errors.PULSE_OVERLAY_REF_UNKNOWN),
			"overlay "+string(spec.Kind)+" baseline-index position must be non-negative",
			map[string]any{
				"index":          index,
				"kind":           string(spec.Kind),
				"baseline_index": ref.Position,
				"series_length":  baselineIndexSeriesLengthDetail(predictedLength),
			})
		return
	}
	if predictedLength < 0 {
		// Schema cannot bound the host series length (non-categorical
		// grouper or other runtime-derivable surface). Defer to the
		// runtime resolver.
		return
	}
	if ref.Position >= predictedLength {
		env.AddError(string(errors.PULSE_OVERLAY_REF_UNKNOWN),
			"overlay "+string(spec.Kind)+" baseline-index position exceeds the predicted host series length",
			map[string]any{
				"index":          index,
				"kind":           string(spec.Kind),
				"baseline_index": ref.Position,
				"series_length":  predictedLength,
			})
		return
	}
}

// overlayBaselineIndexPredictedSeriesLength returns the upper-bound
// predicted series length for the SERIES host implied by req, computed
// purely from schema state (dict cardinalities). Returns -1 when the
// schema cannot bound the host length (any grouper is non-categorical,
// schema is nil, or the host is not a SERIES host).
//
// Today the schema-derivable surface is GROUP_CATEGORY over a
// categorical_* field — the dict count is the upper bound for that
// axis. GROUP_DATE / GROUP_RANGE / GROUP_ROUNDED / GROUP_QUANTILE all
// produce bin counts that depend on the cohort content or
// caller-supplied bin width, so the gate defers to runtime for those.
// Multi-grouper hosts multiply per-axis dict counts (cartesian upper
// bound); a single non-categorical grouper anywhere in the list yields
// -1 (the cartesian is non-computable).
func overlayBaselineIndexPredictedSeriesLength(req *types.Request, schema *encoding.Schema) int {
	if req == nil || schema == nil {
		return -1
	}
	if len(req.Groups) == 0 {
		return -1
	}
	product := 1
	for _, g := range req.Groups {
		if g == nil {
			return -1
		}
		if g.Type != types.GROUP_CATEGORY {
			return -1
		}
		f := schema.Field(g.Field)
		if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
			return -1
		}
		count := f.Dictionary.Count()
		if count <= 0 {
			return -1
		}
		product *= count
		if product <= 0 {
			// Defensive guard against overflow on pathologically wide
			// multi-axis hosts (a 32-bit overflow lands non-positive on
			// most platforms). Treat as non-derivable so the runtime
			// resolver owns the range check.
			return -1
		}
	}
	return product
}

// baselineIndexSeriesLengthDetail returns a JSON-friendly representation
// of the predicted series length for the negative-Position error
// Details map. Negative `predictedLength` (the "not derivable from
// schema" sentinel) surfaces as 0 so the wire detail map carries an
// integer rather than a Go-internal sentinel; the runtime mirror's
// `series_length` slot is always the actual host length.
func baselineIndexSeriesLengthDetail(predictedLength int) int {
	if predictedLength < 0 {
		return 0
	}
	return predictedLength
}

// validateOverlayLevelWithinPredict mirrors the runtime
// processing.validateOverlayLevelWithinRuntime gate at predict time.
// Rules:
//
//   - For the share / index / delta / zscore family Level / Within
//     are each in `[0, axisDepth)` for their respective axis. The
//     axis Level addresses is the same axis the overlay is centerpoint-
//     locked to; Within addresses the OPPOSITE axis. Out-of-range
//     fires PULSE_OVERLAY_LEVEL_OUT_OF_RANGE with Details carrying
//     the spec index, kind, level / within, and axis depth.
//
//   - For the χ² / Fisher inferential family Level / Within MUST be
//     zero — those handlers compute their own contingency from the
//     host margins inline and Level / Within would alter the implicit-
//     margin contract. Non-zero values fire
//     PULSE_OVERLAY_LEVEL_OUT_OF_RANGE.
//
//   - When req.Crosstab is nil the gate skips (the per-kind ref/scope
//     check already surfaced PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE).
//
// Zero defaults (Level == 0 && Within == 0) pass — the runtime
// resolver short-circuits to the legacy MarginFor lookup, preserving
// the E1 / E2-S1..S9 byte-identity contract.
func validateOverlayLevelWithinPredict(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	if spec == nil {
		return
	}
	// INDEX_VS_TOTAL is a SERIES-host kind (req.Groups, no req.Crosstab);
	// its Level / Within gate is independent of the crosstab axis depths
	// since the implicit-grand-total denominator does not partition by
	// any axis prefix. Run the gate before the no-crosstab short-circuit
	// so the rule still fires when Request.Crosstab is nil.
	if spec.Kind == types.OverlayKindIndexVsTotal {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (implicit-grand-total kind)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// DELTA_VS_SIBLING / INDEX_VS_SIBLING are SERIES-host kinds
	// (req.Groups, no req.Crosstab) whose Level / Within gate mirrors
	// INDEX_VS_TOTAL because the sibling reference is a SINGLE FIXED
	// group identified by Ref.Sibling.{Field, Value}, NOT an axis-
	// prefix denominator. Level / Within would alter the implicit
	// sibling-reference contract (the resolver would have to descend
	// into a prefix-bucket rather than match a single group), which
	// is out of scope for v1 of the sibling family. Non-zero values
	// fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. Run the gate before the
	// no-crosstab short-circuit so the rule still fires when
	// Request.Crosstab is nil.
	if spec.Kind == types.OverlayKindDeltaVsSibling || spec.Kind == types.OverlayKindIndexVsSibling {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (sibling reference is a single fixed group)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// INDEX_VS_PRIOR is the E4-S4 windowed-SERIES kind (req.Groups, no
	// req.Crosstab); its Level / Within gate mirrors INDEX_VS_TOTAL
	// because the single-state lag carrier folds across the ordered axis
	// without a prefix-bucket denominator. Run the gate before the no-
	// crosstab short-circuit so the rule still fires when
	// Request.Crosstab is nil. Implicit-margin / windowed family rule.
	if spec.Kind == types.OverlayKindIndexVsPrior {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (windowed lag carrier folds across the ordered axis without a prefix-bucket denominator)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// ZSCORE_VS_TOTAL is a SERIES-host kind (req.Groups, no req.Crosstab);
	// its Level / Within gate mirrors INDEX_VS_TOTAL because the
	// implicit-grand-total mean + SD do not partition by any axis prefix.
	// Run the gate before the no-crosstab short-circuit so the rule still
	// fires when Request.Crosstab is nil. Sibling rule to the
	// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES dispatch above.
	if spec.Kind == types.OverlayKindZScoreVsTotal {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (implicit-grand-total kind)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// SHARE_OF_TOTAL SERIES dispatch (E3-S3) honours the same implicit-
	// grand-total contract as INDEX_VS_TOTAL — Level / Within must both
	// be zero. The MATRIX dispatch (E2-S3) falls through to the
	// crosstab-axis-depth check below where the kind's row+col depths
	// are accepted (the existing E2-S11 rule), preserving the byte-
	// identity contract with pre-E3 SHARE_OF_TOTAL MATRIX requests.
	// Host-shape disambiguation matches `validateOverlayShareOfTotal`'s
	// dispatch policy.
	if spec.Kind == types.OverlayKindShareOfTotal && req != nil && req.Crosstab == nil && len(req.Groups) > 0 {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" SERIES dispatch does not support Level / Within (implicit-grand-total contract)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
					"host":   "series",
				})
		}
		return
	}
	if req == nil || req.Crosstab == nil {
		return
	}
	switch spec.Kind {
	case types.OverlayKindChiSqCol,
		types.OverlayKindChiSqMatrix,
		types.OverlayKindChiSqRow,
		types.OverlayKindFisherExactCell:
		// Inferential family — Level / Within must both be zero.
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (implicit-margin inferential kind)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// Share / index / delta / zscore family — Level on SAME axis,
	// Within on OPPOSITE axis. Resolve the axis pair off the kind so
	// the gate stays kind-aware (mirrors the runtime dispatch).
	levelAxisDepth, withinAxisDepth, levelAxisLabel, withinAxisLabel := overlayLevelWithinAxisDepthsPredict(spec, req)
	if spec.Level < 0 || (levelAxisDepth > 0 && spec.Level >= levelAxisDepth) {
		env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
			"overlay "+string(spec.Kind)+" Level is out of range for the "+levelAxisLabel+" axis",
			map[string]any{
				"index":      index,
				"kind":       string(spec.Kind),
				"level":      spec.Level,
				"axis":       levelAxisLabel,
				"axis_depth": levelAxisDepth,
			})
	}
	if spec.Within < 0 || (withinAxisDepth > 0 && spec.Within >= withinAxisDepth) {
		env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
			"overlay "+string(spec.Kind)+" Within is out of range for the "+withinAxisLabel+" axis",
			map[string]any{
				"index":      index,
				"kind":       string(spec.Kind),
				"within":     spec.Within,
				"axis":       withinAxisLabel,
				"axis_depth": withinAxisDepth,
			})
	}
}

// overlayLevelWithinAxisDepthsPredict resolves which crosstab axis the
// spec's Level / Within slots address. Returns the axis depths
// (len(Rows) / len(Columns)) and axis labels ("rows" / "columns") for
// the error details.
//
//   - SHARE_OF_ROW: Level on ROW axis, Within on COLUMN axis.
//   - SHARE_OF_COL: Level on COLUMN axis, Within on ROW axis.
//   - SHARE_OF_TOTAL: Level / Within nominally on grand; the helper
//     returns row + column depths so non-zero values are accepted
//     within the row / column range (SHARE_OF_TOTAL ignores Level /
//     Within at runtime but the predict gate does not need to reject
//     them — they are inert).
//   - INDEX_VS_MARGIN / DELTA_VS_MARGIN / ZSCORE_VS_MARGIN: axis is
//     driven by Ref.Margin.Axis; Level addresses that axis and
//     Within addresses the opposite one.
func overlayLevelWithinAxisDepthsPredict(spec *types.OverlaySpec, req *types.Request) (
	levelAxisDepth, withinAxisDepth int,
	levelAxisLabel, withinAxisLabel string,
) {
	if req == nil || req.Crosstab == nil {
		return 0, 0, "rows", "columns"
	}
	rowDepth := len(req.Crosstab.Rows)
	colDepth := len(req.Crosstab.Columns)
	switch spec.Kind {
	case types.OverlayKindShareOfRow:
		return rowDepth, colDepth, "rows", "columns"
	case types.OverlayKindShareOfCol:
		return colDepth, rowDepth, "columns", "rows"
	case types.OverlayKindShareOfTotal:
		return rowDepth, colDepth, "rows", "columns"
	case types.OverlayKindIndexVsMargin,
		types.OverlayKindDeltaVsMargin,
		types.OverlayKindZScoreVsMargin:
		if spec.Ref.Margin == nil {
			return 0, 0, "rows", "columns"
		}
		switch spec.Ref.Margin.Axis {
		case types.MarginAxisRow:
			return rowDepth, colDepth, "rows", "columns"
		case types.MarginAxisColumn:
			return colDepth, rowDepth, "columns", "rows"
		case types.MarginAxisGrand:
			return rowDepth, colDepth, "rows", "columns"
		}
	}
	return 0, 0, "rows", "columns"
}

// validateOverlaySpec applies the E1 ruleset to one OverlaySpec.
// Errors are emitted with deterministic Details so MCP / CLI
// envelopes can render the index, kind, and offending value without
// re-parsing the message string.
func validateOverlaySpec(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	if spec == nil {
		return
	}

	// Unknown-kind check first — every other rule is keyed by Kind so
	// we cannot reasonably validate Scope / Ref against an unknown
	// catalog entry. OverlayStreamable(known=false) is the authoritative
	// "is this kind in the catalog?" probe; AllOverlayKinds() and the
	// streamability table are co-maintained per TestStreamability_OverlaysKnown.
	if _, known := types.OverlayStreamable(spec.Kind); !known {
		env.AddError(string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
			"overlay kind is not in the catalog: "+string(spec.Kind),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	switch spec.Kind {
	case types.OverlayKindChiSqCol:
		validateOverlayChiSqCol(env, req, spec, index)
	case types.OverlayKindChiSqMatrix:
		validateOverlayChiSqMatrix(env, req, spec, index)
	case types.OverlayKindChiSqRow:
		validateOverlayChiSqRow(env, req, spec, index)
	case types.OverlayKindDeltaVsMargin:
		validateOverlayDeltaVsMargin(env, req, spec, index)
	case types.OverlayKindDeltaVsSibling:
		validateOverlayDeltaVsSibling(env, req, spec, index)
	case types.OverlayKindFisherExactCell:
		validateOverlayFisherExactCell(env, req, spec, index)
	case types.OverlayKindIndexVsMargin:
		validateOverlayIndexVsMargin(env, req, spec, index)
	case types.OverlayKindIndexVsPrior:
		validateOverlayIndexVsPrior(env, req, spec, index)
	case types.OverlayKindIndexVsSibling:
		validateOverlayIndexVsSibling(env, req, spec, index)
	case types.OverlayKindIndexVsTotal:
		validateOverlayIndexVsTotal(env, req, spec, index)
	case types.OverlayKindShareOfCol:
		validateOverlayShareOfCol(env, req, spec, index)
	case types.OverlayKindShareOfRow:
		validateOverlayShareOfRow(env, req, spec, index)
	case types.OverlayKindShareOfTotal:
		validateOverlayShareOfTotal(env, req, spec, index)
	case types.OverlayKindZScoreVsMargin:
		validateOverlayZScoreVsMargin(env, req, spec, index)
	case types.OverlayKindZScoreVsTotal:
		validateOverlayZScoreVsTotal(env, req, spec, index)
	}
}

// validateOverlayIndexVsMargin enforces the per-kind contract for
// OVERLAY_INDEX_VS_MARGIN: Ref must populate Margin, Margin.Axis must
// be a known MarginAxis, the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), and Scope must be in the supported
// set. Every condition emits a distinct error so a caller surfacing
// multiple structural problems sees them all in one pass.
func validateOverlayIndexVsMargin(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref family must be Margin. The OverlayRef union allows multiple
	// reserved pointers; only Margin is meaningful for INDEX_VS_MARGIN
	// today. A missing Margin pointer is a shape mismatch — the
	// caller asked for "compare against the row margin" without
	// telling us which margin to compare against, OR without a
	// crosstab host to host the margin.
	if spec.Ref.Margin == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Margin (axis-margin reference)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Margin.Axis must be a known MarginAxis. Unknown values are a
	// shape mismatch in the same family — the validator cannot resolve
	// the margin slot if it does not know which one is targeted.
	if !validMarginAxes[spec.Ref.Margin.Axis] {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Margin.Axis is not a known MarginAxis: "+string(spec.Ref.Margin.Axis),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Host must be MATRIX-shaped. Today the only MATRIX-shaped host is
	// req.Crosstab; without it there are no margin slots for the overlay
	// to reference. Future kinds may broaden the matrix-host predicate
	// (e.g. when group-result overlays land), but INDEX_VS_MARGIN
	// specifically derives its denominator from a crosstab margin.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Scope must be in the supported set for INDEX_VS_MARGIN. E1
	// supports CELL only; ROW / COLUMN / TOTAL ship in later epics
	// alongside the matching payload shapes.
	if !indexVsMarginSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (E1 supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayIndexVsTotal enforces the per-kind contract for
// OVERLAY_INDEX_VS_TOTAL: the Ref union must be EMPTY (implicit-
// grand-total — the host series' own grand total is the denominator),
// the host result must be SERIES-shaped (i.e. Request.Groups is non-
// empty and Request.Crosstab is nil), and Scope must be GROUP.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when Request.Crosstab
//     is non-nil (the kind targets a SERIES host, not a MATRIX one) OR
//     when Request.Groups is empty (no host series to compute against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than GROUP.
func validateOverlayIndexVsTotal(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref must be empty — INDEX_VS_TOTAL is implicit-grand-total. A
	// caller supplying any family pointer (Margin / Sibling /
	// BaselineIndex / Population / Stage / Slot) is using the wrong
	// overlay shape.
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-grand-total: the host series' own grand total is the denominator)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	// A request with no groupers has no series to compute against.
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a SERIES host (grouped Process result: Request.Groups non-empty, Request.Crosstab nil)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Scope must be GROUP. INDEX_VS_TOTAL emits one entry per host
	// group key — CELL / ROW / COLUMN / MATRIX / TOTAL scopes are not
	// meaningful for the per-group statistic the kind emits.
	if !indexVsTotalSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: group)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayIndexVsPrior enforces the per-kind contract for
// OVERLAY_INDEX_VS_PRIOR (E4-S4, first windowed-Process kind in the
// catalog and first consumer of the `Ref.Prior` arm of the discriminated
// OverlayRef union):
//
//   - Ref.Prior populated → accepted. Ref.Prior.Lag MUST be zero (v1
//     ships lag-1 only via the implicit-default arm; the slot is
//     forward-compat for future window-N priors).
//   - Ref entirely empty → accepted (the implicit-default authoring
//     shape — both spellings spell "lag-1 prior").
//   - Any other ref-family pointer populated (Margin / Sibling /
//     BaselineIndex / Population / Stage / Slot) → reject with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Host must be SERIES-shaped (Request.Crosstab nil AND
//     Request.Groups non-empty — the kind targets a windowed ordered-
//     axis SERIES host, not a MATRIX one).
//   - Scope must be GROUP (mirrors INDEX_VS_TOTAL / SHARE_OF_TOTAL
//     SERIES / ZSCORE_VS_TOTAL).
//
// Level / Within rule lives in validateOverlayLevelWithinPredict — the
// kind is in the implicit-margin / windowed family because the lag
// carrier folds across the ordered axis without a prefix-bucket
// denominator, so non-zero Level / Within values fire
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. The runtime mirror
// (processing.validateOverlayLevelWithinRuntime) enforces the same
// rule.
func validateOverlayIndexVsPrior(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref family: Prior populated OR entire Ref empty. Any other family
	// pointer is a shape mismatch (mirrors the INDEX_VS_TOTAL /
	// SHARE_OF_TOTAL SERIES implicit-default rejection set).
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Prior or an empty Ref (windowed lag-1 prior; no Margin / Sibling / BaselineIndex / Population / Stage / Slot)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Ref.Prior populated: Lag MUST be zero for v1. The slot is reserved
	// for future window-N priors; non-zero values land in a later story
	// and the carrier widens from a single f64 to a small ring buffer
	// then.
	if spec.Ref.Prior != nil && spec.Ref.Prior.Lag != 0 {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Prior.Lag must be zero (v1 ships lag-1 only; the slot is reserved for future window-N priors)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"lag":   spec.Ref.Prior.Lag,
			})
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a SERIES host (grouped Process result: Request.Groups non-empty, Request.Crosstab nil)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Scope must be GROUP. INDEX_VS_PRIOR emits one entry per host group
	// key (the ordered windowed series) — CELL / ROW / COLUMN / MATRIX /
	// TOTAL scopes are not meaningful for the per-group statistic the
	// kind emits.
	if !indexVsPriorSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: group)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayChiSqMatrix enforces the per-kind contract for
// OVERLAY_CHISQ_MATRIX: the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), Scope must be MATRIX, and the Ref
// union must be EMPTY — unlike the Margin-bearing CELL overlays the
// χ² independence test is implicit-margin and uses the host's row /
// column / grand margins inline. A caller-supplied Ref.Margin (or any
// other ref-family pointer) is a shape mismatch.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is
//     nil (no MATRIX host to test against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than MATRIX.
func validateOverlayChiSqMatrix(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref must be empty — CHISQ_MATRIX is implicit-margin. A caller
	// supplying any family pointer (Margin / Sibling / BaselineIndex /
	// Population / Stage / Slot) is using the wrong overlay shape.
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: the χ² test uses the host's row / column / grand margins inline)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Host must be MATRIX-shaped — χ² independence consumes a row ×
	// column contingency table sourced from the host crosstab.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Scope must be MATRIX. CHISQ_MATRIX is a whole-table test —
	// CELL / ROW / COLUMN / TOTAL / GROUP are not meaningful for the
	// statistic the kind emits.
	if !chiSqMatrixSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: matrix)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayChiSqRow enforces the per-kind contract for
// OVERLAY_CHISQ_ROW: the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), Scope must be ROW, and the Ref union
// must be EMPTY — the per-row χ² goodness-of-fit test is implicit-
// margin and uses the host's row / column / grand margins inline
// (mirrors the CHISQ_MATRIX contract). A caller-supplied Ref.Margin
// (or any other ref-family pointer) is a shape mismatch.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is
//     nil (no MATRIX host to test against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than ROW.
func validateOverlayChiSqRow(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref must be empty — CHISQ_ROW is implicit-margin (same contract
	// as CHISQ_MATRIX). A caller supplying any family pointer (Margin /
	// Sibling / BaselineIndex / Population / Stage / Slot) is using the
	// wrong overlay shape.
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: the per-row χ² test uses the host's row / column / grand margins inline)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Host must be MATRIX-shaped — per-row χ² goodness-of-fit consumes
	// a row × column contingency table sourced from the host crosstab.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Scope must be ROW. CHISQ_ROW emits one statistic per row tuple —
	// CELL / COLUMN / MATRIX / TOTAL / GROUP are not meaningful for the
	// per-row statistic the kind emits.
	if !chiSqRowSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: row)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayChiSqCol enforces the per-kind contract for
// OVERLAY_CHISQ_COL: the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), Scope must be COLUMN, and the Ref union
// must be EMPTY — the per-column χ² goodness-of-fit test is implicit-
// margin and uses the host's row / column / grand margins inline
// (mirrors the CHISQ_MATRIX / CHISQ_ROW contract). A caller-supplied
// Ref.Margin (or any other ref-family pointer) is a shape mismatch.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is
//     nil (no MATRIX host to test against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than COLUMN.
func validateOverlayChiSqCol(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref must be empty — CHISQ_COL is implicit-margin (mechanical
	// column-axis twin of CHISQ_ROW; same contract as CHISQ_MATRIX).
	// A caller supplying any family pointer (Margin / Sibling /
	// BaselineIndex / Population / Stage / Slot) is using the wrong
	// overlay shape.
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: the per-column χ² test uses the host's row / column / grand margins inline)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Host must be MATRIX-shaped — per-column χ² goodness-of-fit
	// consumes a row × column contingency table sourced from the host
	// crosstab.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Scope must be COLUMN. CHISQ_COL emits one statistic per column
	// tuple — CELL / ROW / MATRIX / TOTAL / GROUP are not meaningful
	// for the per-column statistic the kind emits.
	if !chiSqColSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: column)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayDeltaVsMargin enforces the per-kind contract for
// OVERLAY_DELTA_VS_MARGIN: Ref must populate Margin (the axis-margin
// reference family), Margin.Axis must be a known MarginAxis (any of
// row / column / grand — unlike the SHARE_OF_* triad the runtime
// handler dispatches all three axes, mirroring INDEX_VS_MARGIN and
// ZSCORE_VS_MARGIN), the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), and Scope must be CELL.
func validateOverlayDeltaVsMargin(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref family must be Margin. DELTA_VS_MARGIN shares the same ref
	// shape contract as INDEX_VS_MARGIN / SHARE_OF_* / ZSCORE_VS_MARGIN
	// — the centerpoint is an axis-margin slot.
	if spec.Ref.Margin == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Margin (axis-margin reference)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Margin.Axis must be a known MarginAxis. DELTA_VS_MARGIN accepts
	// every known axis at predict time AND at runtime (the runtime
	// handler dispatches the matching margin); unknown values are a
	// shape mismatch.
	if !validMarginAxes[spec.Ref.Margin.Axis] {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Margin.Axis is not a known MarginAxis: "+string(spec.Ref.Margin.Axis),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Host must be MATRIX-shaped — delta-vs-margin needs a crosstab
	// margin slot to subtract from each cell.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Scope must be CELL. DELTA_VS_MARGIN is a cell-decoration overlay
	// by construction.
	if !deltaVsMarginSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayFisherExactCell enforces the per-kind contract for
// OVERLAY_FISHER_EXACT_CELL: the host result must be MATRIX-shaped
// (i.e. Request.Crosstab is non-nil), Scope must be CELL, and the Ref
// union must be EMPTY — the per-cell 2×2 Fisher's exact test is
// implicit-margin and reads the host's row + column margins inline
// (mirrors the CHISQ_* implicit-margin contract). A caller-supplied
// Ref.Margin (or any other ref-family pointer) is a shape mismatch.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is
//     nil (no MATRIX host to test against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than CELL.
func validateOverlayFisherExactCell(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref must be empty — FISHER_EXACT_CELL is implicit-margin (mirrors
	// the CHISQ_* family). A caller supplying any family pointer
	// (Margin / Sibling / BaselineIndex / Population / Stage / Slot)
	// is using the wrong overlay shape.
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: the per-cell 2×2 Fisher's exact test reads the host's row + column margins inline)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Host must be MATRIX-shaped — per-cell Fisher's exact consumes a
	// row × column contingency table sourced from the host crosstab.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Scope must be CELL. FISHER_EXACT_CELL emits one p-value per cell
	// — ROW / COLUMN / MATRIX / TOTAL / GROUP scopes are not meaningful
	// for the per-cell statistic the kind emits.
	if !fisherExactCellSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayShareOfRow enforces the per-kind contract for
// OVERLAY_SHARE_OF_ROW: Ref must populate Margin (the row-margin
// reference family), Margin.Axis must be a known MarginAxis (the
// runtime handler is row-axis-locked, but the validator accepts any
// known axis at predict time so a misconfigured caller fails closed
// with a single shape-mismatch code rather than a stricter "must be
// row" code), the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), and Scope must be CELL.
func validateOverlayShareOfRow(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref family must be Margin. SHARE_OF_ROW shares the same ref
	// shape contract as INDEX_VS_MARGIN — the denominator is an axis-
	// margin slot.
	if spec.Ref.Margin == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Margin (axis-margin reference)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Margin.Axis must be a known MarginAxis. SHARE_OF_ROW is row-
	// axis-locked at runtime, but the predict gate accepts any known
	// axis to keep the failure modes orthogonal — an unknown axis is
	// "shape mismatch", not "kind mismatch".
	if !validMarginAxes[spec.Ref.Margin.Axis] {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Margin.Axis is not a known MarginAxis: "+string(spec.Ref.Margin.Axis),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Host must be MATRIX-shaped — share-of-row needs a crosstab row
	// margin to divide by.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Scope must be CELL. SHARE_OF_ROW is a cell-decoration overlay
	// by construction.
	if !shareOfRowSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayShareOfCol enforces the per-kind contract for
// OVERLAY_SHARE_OF_COL: Ref must populate Margin (the column-margin
// reference family), Margin.Axis must be a known MarginAxis (the
// runtime handler is column-axis-locked, but the validator accepts
// any known axis at predict time so a misconfigured caller fails
// closed with a single shape-mismatch code rather than a stricter
// "must be column" code — matches the SHARE_OF_ROW followup policy),
// the host result must be MATRIX-shaped (i.e. Request.Crosstab is
// non-nil), and Scope must be CELL.
func validateOverlayShareOfCol(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref family must be Margin. SHARE_OF_COL shares the same ref
	// shape contract as INDEX_VS_MARGIN / SHARE_OF_ROW — the
	// denominator is an axis-margin slot.
	if spec.Ref.Margin == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Margin (axis-margin reference)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Margin.Axis must be a known MarginAxis. SHARE_OF_COL is column-
	// axis-locked at runtime, but the predict gate accepts any known
	// axis to keep the failure modes orthogonal — an unknown axis is
	// "shape mismatch", not "kind mismatch".
	if !validMarginAxes[spec.Ref.Margin.Axis] {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Margin.Axis is not a known MarginAxis: "+string(spec.Ref.Margin.Axis),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Host must be MATRIX-shaped — share-of-column needs a crosstab
	// column margin to divide by.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Scope must be CELL. SHARE_OF_COL is a cell-decoration overlay
	// by construction.
	if !shareOfColSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayShareOfTotal enforces the per-kind contract for
// OVERLAY_SHARE_OF_TOTAL. The kind is dual-shape — the dispatch chooses
// between the MATRIX-host validator (E2-S3) and the SERIES-host
// validator (E3-S3) based on the request's host shape:
//
//   - Request.Crosstab non-nil ⇒ MATRIX dispatch: Ref must populate
//     Margin (the grand-total reference family), Margin.Axis must be a
//     known MarginAxis, host must be MATRIX-shaped, Scope must be CELL.
//   - Request.Crosstab nil + Request.Groups non-empty ⇒ SERIES
//     dispatch: Ref must be empty (implicit-grand-total — sibling rule
//     to OVERLAY_INDEX_VS_TOTAL), Scope must be GROUP.
//   - Neither host shape present ⇒ MATRIX-style rejection so the
//     existing failure mode is preserved (callers without a host get
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE the same way they did
//     before E3-S3 landed).
//
// Host-shape disambiguation by inspecting `req.Crosstab` / `req.Groups`
// matches `validateOverlayIndexVsTotal`'s policy and the runtime
// dispatch in `processing/overlay_series.go` (ApplyOverlaysSeries) vs
// `processing/overlay.go` (ApplyOverlays) — each runtime path consumes
// its own dispatch table, so the predict-time validator has to make the
// same routing decision.
func validateOverlayShareOfTotal(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// SERIES-host dispatch path — Request.Crosstab nil AND
	// Request.Groups non-empty. The SERIES dispatch is implicit-grand-
	// total so the Ref union MUST be empty (mirrors the INDEX_VS_TOTAL
	// rule). Falls through to MATRIX-style validation otherwise.
	if req != nil && req.Crosstab == nil && len(req.Groups) > 0 {
		validateOverlayShareOfTotalSeries(env, req, spec, index)
		return
	}
	validateOverlayShareOfTotalMatrix(env, req, spec, index)
}

// validateOverlayShareOfTotalMatrix enforces the MATRIX-host dispatch
// (E2-S3): Ref must populate Margin (the grand-total reference family),
// Margin.Axis must be a known MarginAxis (the runtime handler is grand-
// axis-locked, but the validator accepts any known axis at predict time
// so a misconfigured caller fails closed with a single shape-mismatch
// code rather than a stricter "must be grand" code — matches the
// SHARE_OF_ROW / SHARE_OF_COL followup policy), the host result must be
// MATRIX-shaped (i.e. Request.Crosstab is non-nil), and Scope must be
// CELL.
func validateOverlayShareOfTotalMatrix(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref family must be Margin. SHARE_OF_TOTAL shares the same ref
	// shape contract as INDEX_VS_MARGIN / SHARE_OF_ROW / SHARE_OF_COL
	// — the denominator is an axis-margin slot (the grand axis).
	if spec.Ref.Margin == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Margin (axis-margin reference)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Margin.Axis must be a known MarginAxis. SHARE_OF_TOTAL is grand-
	// axis-locked at runtime, but the predict gate accepts any known
	// axis to keep the failure modes orthogonal — an unknown axis is
	// "shape mismatch", not "kind mismatch".
	if !validMarginAxes[spec.Ref.Margin.Axis] {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Margin.Axis is not a known MarginAxis: "+string(spec.Ref.Margin.Axis),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Host must be MATRIX-shaped — share-of-total needs a crosstab
	// grand-total slot to divide by.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Scope must be CELL. SHARE_OF_TOTAL is a cell-decoration overlay
	// by construction.
	if !shareOfTotalSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayShareOfTotalSeries enforces the SERIES-host dispatch
// (E3-S3): the Ref union must be EMPTY (implicit-grand-total — the host
// series' own grand total is the denominator), the host result must be
// SERIES-shaped (Request.Crosstab nil AND Request.Groups non-empty —
// the caller-side dispatcher already verified the host shape before
// routing here, so we re-assert in case a future caller calls into this
// branch directly), and Scope must be GROUP.
//
// Sibling rule to validateOverlayIndexVsTotal — the two SERIES SHARE /
// INDEX kinds share the implicit-grand-total contract verbatim.
func validateOverlayShareOfTotalSeries(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref must be empty — SERIES SHARE_OF_TOTAL is implicit-grand-total
	// (mirrors INDEX_VS_TOTAL). A caller supplying any family pointer
	// (Margin / Sibling / BaselineIndex / Population / Stage / Slot) is
	// using the MATRIX dispatch's spec shape against a SERIES host.
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" against a SERIES host must leave Ref empty (implicit-grand-total: the host series' own grand total is the denominator)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"host":  "series",
			})
		return
	}

	// Belt-and-suspenders host check — the caller already asserted this,
	// but re-check so a direct call into this branch from a future
	// validator-aware caller still gets the right rejection shape.
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" SERIES dispatch requires a SERIES host (grouped Process result: Request.Groups non-empty, Request.Crosstab nil)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"host":  "series",
			})
		return
	}

	// Scope must be GROUP. SERIES SHARE_OF_TOTAL emits one entry per
	// host group key — CELL / ROW / COLUMN / MATRIX / TOTAL scopes are
	// not meaningful for the per-group statistic the SERIES dispatch
	// emits.
	if !shareOfTotalSeriesSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" SERIES dispatch does not support scope "+string(spec.Scope)+" (supports: group)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
				"host":  "series",
			})
		return
	}
}

// validateOverlayZScoreVsMargin enforces the per-kind contract for
// OVERLAY_ZSCORE_VS_MARGIN: Ref must populate Margin (the axis-margin
// reference family), Margin.Axis must be a known MarginAxis (any of
// row / column / grand — unlike the SHARE_OF_* triad the runtime
// handler dispatches all three axes), the host result must be MATRIX-
// shaped (i.e. Request.Crosstab is non-nil), and Scope must be CELL.
func validateOverlayZScoreVsMargin(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref family must be Margin. ZSCORE_VS_MARGIN shares the same ref
	// shape contract as INDEX_VS_MARGIN / SHARE_OF_* — the centerpoint
	// is an axis-margin slot.
	if spec.Ref.Margin == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Margin (axis-margin reference)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Margin.Axis must be a known MarginAxis. ZSCORE_VS_MARGIN
	// accepts every known axis at predict time AND at runtime (the
	// runtime handler dispatches the matching slice); unknown values
	// are a shape mismatch.
	if !validMarginAxes[spec.Ref.Margin.Axis] {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Margin.Axis is not a known MarginAxis: "+string(spec.Ref.Margin.Axis),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Host must be MATRIX-shaped — z-score-vs-margin needs a crosstab
	// margin slot to subtract from each cell AND a crosstab cell grid
	// to drive the per-slice Welford recurrence.
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"axis":  string(spec.Ref.Margin.Axis),
			})
		return
	}

	// Scope must be CELL. ZSCORE_VS_MARGIN is a cell-decoration overlay
	// by construction.
	if !zscoreVsMarginSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayZScoreVsTotal enforces the per-kind contract for
// OVERLAY_ZSCORE_VS_TOTAL: the Ref union must be EMPTY (implicit-
// grand-total — the host series' own grand-total mean + SD are the
// centerpoint), the host result must be SERIES-shaped (i.e.
// Request.Groups is non-empty and Request.Crosstab is nil), and Scope
// must be GROUP. Sibling validator to validateOverlayIndexVsTotal and
// validateOverlayShareOfTotalSeries — the third streamable SERIES-host
// kind in the E3 grouped-Process subset shares the implicit-grand-total
// contract verbatim.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when Request.Crosstab
//     is non-nil (the kind targets a SERIES host, not a MATRIX one) OR
//     when Request.Groups is empty (no host series to standardise
//     against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than GROUP.
func validateOverlayZScoreVsTotal(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref must be empty — ZSCORE_VS_TOTAL is implicit-grand-total
	// (mirrors INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES). A caller
	// supplying any family pointer (Margin / Sibling / BaselineIndex /
	// Population / Stage / Slot) is using the wrong overlay shape.
	if spec.Ref.Margin != nil ||
		spec.Ref.Sibling != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-grand-total: the host series' own grand-total mean + SD are the centerpoint)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	// A request with no groupers has no series to standardise against.
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a SERIES host (grouped Process result: Request.Groups non-empty, Request.Crosstab nil)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Scope must be GROUP. ZSCORE_VS_TOTAL emits one entry per host
	// group key — CELL / ROW / COLUMN / MATRIX / TOTAL scopes are not
	// meaningful for the per-group statistic the kind emits.
	if !zscoreVsTotalSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: group)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}

// validateOverlayDeltaVsSibling enforces the per-kind contract for
// OVERLAY_DELTA_VS_SIBLING: the Ref family must be Sibling (not
// Margin / BaselineIndex / Population / Stage / Slot), Sibling.Field
// and Sibling.Value must both be non-empty, the host result must be
// SERIES-shaped (i.e. Request.Groups is non-empty and Request.Crosstab
// is nil), and Scope must be GROUP. Sibling validator to
// `validateOverlayIndexVsSibling`.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any non-Sibling
//     family pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when Ref.Sibling is
//     nil OR when its Field / Value are empty (the sibling pair is the
//     denominator anchor and both halves are required).
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when Request.Crosstab
//     is non-nil (the kind targets a SERIES host) OR when
//     Request.Groups is empty (no host series to compute against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than GROUP.
func validateOverlayDeltaVsSibling(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	validateOverlaySiblingKind(env, req, spec, index, deltaVsSiblingSupportedScopes, "group")
}

// validateOverlayIndexVsSibling enforces the per-kind contract for
// OVERLAY_INDEX_VS_SIBLING. Twin validator to
// `validateOverlayDeltaVsSibling` — the two sibling-reference kinds
// share an identical predict-time contract; only the runtime math
// differs (subtraction vs ratio scaling). Routes through the same
// `validateOverlaySiblingKind` helper so a future schema/runtime
// rule that fires on one kind automatically extends to the other.
func validateOverlayIndexVsSibling(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	validateOverlaySiblingKind(env, req, spec, index, indexVsSiblingSupportedScopes, "group")
}

// validateOverlaySiblingKind is the shared predict-time validator for
// the sibling-reference SERIES-host family (DELTA_VS_SIBLING +
// INDEX_VS_SIBLING). The two kinds carry identical structural
// contracts — only the runtime math differs (subtraction vs ratio
// scaling) — so the validator collapses both into a single helper
// keyed by the kind's supported-scope set.
//
// Contract enforced (in order, first hit short-circuits):
//
//   - Ref family must be Sibling. Any other ref-family pointer
//     (Margin / BaselineIndex / Population / Stage / Slot) is a
//     shape mismatch (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE).
//   - Ref.Sibling MUST be non-nil; Sibling.Field and Sibling.Value
//     MUST both be non-empty strings. The sibling reference is the
//     denominator anchor and both halves are required.
//   - Host MUST be SERIES-shaped (Request.Crosstab nil AND
//     Request.Groups non-empty). A request with no groupers has no
//     series to compute against; an active crosstab routes down the
//     MATRIX-host path which the sibling family does not target in
//     v1.
//   - Scope MUST be in the supported set (GROUP today).
func validateOverlaySiblingKind(
	env *Envelope, req *types.Request, spec *types.OverlaySpec, index int,
	supportedScopes map[types.OverlayScope]bool, supportedScopeLabel string,
) {
	// Ref family must be Sibling — reject any other family pointer.
	if spec.Ref.Margin != nil ||
		spec.Ref.BaselineIndex != nil ||
		spec.Ref.Population != nil ||
		spec.Ref.Stage != nil ||
		spec.Ref.Slot != nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Sibling only (no Margin / BaselineIndex / Population / Stage / Slot)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Ref.Sibling is required — both Field and Value must be populated.
	if spec.Ref.Sibling == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires Ref.Sibling (sibling-group reference)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}
	if spec.Ref.Sibling.Field == "" {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Sibling.Field is empty (sibling reference requires a grouper Field name)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}
	if spec.Ref.Sibling.Value == "" {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" Ref.Sibling.Value is empty (sibling reference requires a specific axis-key value)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"field": spec.Ref.Sibling.Field,
			})
		return
	}

	// Host must be SERIES-shaped — grouped Process result.
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a SERIES host (grouped Process result: Request.Groups non-empty, Request.Crosstab nil)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	// Scope must be in the supported set.
	if !supportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: "+supportedScopeLabel+")",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}
