package descriptor

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Overlay validator — no-execute, header-and-schema-only validation
// for Request.Overlays specs. Walks every OverlaySpec and surfaces
// structural failures on the envelope alongside the aggregator / test
// / crosstab gates the rest of Predict runs.
//
// E1 scope (kind-catalog-v1 milestone S3):
//
//   - Unknown OverlayKind            → PULSE_OVERLAY_KIND_UNKNOWN
//   - OVERLAY_INDEX_VS_MARGIN
//       * Ref family must be Margin and Margin.Axis must be a known
//         MarginAxis (row / column / grand).
//       * Host result must be MATRIX-shaped, i.e. Request.Crosstab is
//         non-nil. Without a crosstab there is no margin slot to
//         reference; mismatch fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//       * Scope must be one of the supported scopes for the kind. E1
//         supports CELL only; everything else fires
//         PULSE_OVERLAY_SCOPE_UNSUPPORTED. Later epics widen the set
//         as ROW / COLUMN / TOTAL projections land.
//
// Error code strings are local untyped constants in this file; they
// move to errors/codes.go in E1-S7 with no signature change here.
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

// Overlay error code strings — local to this file until E1-S7 promotes
// them to errors.Code constants. Keeping them inline lets E1 ship the
// validator without coupling to the errors package, and the swap in
// E1-S7 is a single grep-and-replace.
const (
	codeOverlayKindUnknown            = "PULSE_OVERLAY_KIND_UNKNOWN"
	codeOverlayRefIncompatibleShape   = "PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE"
	codeOverlayScopeUnsupported       = "PULSE_OVERLAY_SCOPE_UNSUPPORTED"
)

// indexVsMarginSupportedScopes is the E1-supported scope set for
// OVERLAY_INDEX_VS_MARGIN. Today only CELL ships; later epics widen
// the gate to ROW / COLUMN / TOTAL once the matching payload shapes
// are wired through processing.
var indexVsMarginSupportedScopes = map[types.OverlayScope]bool{
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
		env.AddError(codeOverlayKindUnknown,
			"overlay kind is not in the catalog: "+string(spec.Kind),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	switch spec.Kind {
	case types.OverlayKindIndexVsMargin:
		validateOverlayIndexVsMargin(env, req, spec, index)
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
		env.AddError(codeOverlayRefIncompatibleShape,
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
		env.AddError(codeOverlayRefIncompatibleShape,
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
		env.AddError(codeOverlayRefIncompatibleShape,
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
		env.AddError(codeOverlayScopeUnsupported,
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (E1 supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
		return
	}
}
