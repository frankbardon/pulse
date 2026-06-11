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

// shareOfTotalSupportedScopes is the E2-supported scope set for
// OVERLAY_SHARE_OF_TOTAL. SHARE_OF_TOTAL is a CELL-scoped layer by
// construction — every cell divides by the grand total. ROW /
// COLUMN / TOTAL projections are not meaningful for this kind.
var shareOfTotalSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
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
	_ = schema
	_ = opts
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		validateOverlaySpec(env, req, spec, i)
		validateOverlayLevelWithinPredict(env, req, spec, i)
	}
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
	case types.OverlayKindFisherExactCell:
		validateOverlayFisherExactCell(env, req, spec, index)
	case types.OverlayKindIndexVsMargin:
		validateOverlayIndexVsMargin(env, req, spec, index)
	case types.OverlayKindShareOfCol:
		validateOverlayShareOfCol(env, req, spec, index)
	case types.OverlayKindShareOfRow:
		validateOverlayShareOfRow(env, req, spec, index)
	case types.OverlayKindShareOfTotal:
		validateOverlayShareOfTotal(env, req, spec, index)
	case types.OverlayKindZScoreVsMargin:
		validateOverlayZScoreVsMargin(env, req, spec, index)
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
// OVERLAY_SHARE_OF_TOTAL: Ref must populate Margin (the grand-total
// reference family), Margin.Axis must be a known MarginAxis (the
// runtime handler is grand-axis-locked, but the validator accepts any
// known axis at predict time so a misconfigured caller fails closed
// with a single shape-mismatch code rather than a stricter "must be
// grand" code — matches the SHARE_OF_ROW / SHARE_OF_COL followup
// policy), the host result must be MATRIX-shaped (i.e. Request.Crosstab
// is non-nil), and Scope must be CELL.
func validateOverlayShareOfTotal(env *Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
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
