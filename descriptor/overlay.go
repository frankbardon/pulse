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
func ValidateOverlays(env *Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	if req == nil || len(req.Overlays) == 0 {
		return
	}
	_ = schema
	_ = opts
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		validateOverlaySpec(env, req, spec, i)
	}
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
	case types.OverlayKindDeltaVsMargin:
		validateOverlayDeltaVsMargin(env, req, spec, index)
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
