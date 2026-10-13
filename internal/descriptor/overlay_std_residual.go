package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// stdResidualSupportedScopes is the supported scope set for
// OVERLAY_STD_RESIDUAL: one adjusted residual per cell, so CELL only.
var stdResidualSupportedScopes = map[types.OverlayScope]bool{
	types.OverlayScopeCell: true,
}

// stdResidualParts declares the named parts OVERLAY_STD_RESIDUAL writes
// beside its primary residual matrix (types.OverlayPayload.Parts), each
// with its payload shape. overlayPartShapes reads it.
var stdResidualParts = map[string]types.OverlayShape{
	"expected": types.OverlayShapeMatrix,
	"p":        types.OverlayShapeMatrix,
}

// overlayPartShapes is the per-kind table of declared payload parts:
// the Interpretation resolver admits `parts.<name>.cells.value` (MATRIX
// part) or `parts.<name>.scalar` (SCALAR part) only for a part listed
// here. A kind without parts is absent.
var overlayPartShapes = map[types.OverlayKind]map[string]types.OverlayShape{
	types.OverlayKindStdResidual: stdResidualParts,
}

// validateOverlayStdResidual enforces the per-kind contract for
// OVERLAY_STD_RESIDUAL — the OVERLAY_FISHER_EXACT_CELL contract: Ref
// must be EMPTY (implicit-margin: the residuals read the table's own row
// / column / grand totals), the host must be MATRIX-shaped
// (Request.Crosstab) and Scope must be CELL.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is nil.
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything but CELL.
func validateOverlayStdResidual(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	if spec.Ref != (types.OverlayRef{}) {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" must leave Ref empty (implicit-margin: the adjusted residuals read the table's own row / column / grand totals)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}
	if req.Crosstab == nil {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			"overlay "+string(spec.Kind)+" requires a MATRIX host (Request.Crosstab); none present",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}
	if !stdResidualSupportedScopes[spec.Scope] {
		env.AddError(string(errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED),
			"overlay "+string(spec.Kind)+" does not support scope "+string(spec.Scope)+" (supports: cell)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"scope": string(spec.Scope),
			})
	}
}
