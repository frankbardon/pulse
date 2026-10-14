package descriptor

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/types"
)

// stdResidualParts declares the named parts OVERLAY_STD_RESIDUAL writes
// beside its primary residual matrix (types.OverlayPayload.Parts).
// overlayPartShapes reads it.
var stdResidualParts = map[string]overlayPart{
	"expected": {Shape: types.OverlayShapeMatrix},
	"p":        {Shape: types.OverlayShapeMatrix},
}

// overlayPart is one declared payload part: its shape and, when the kind
// writes it only under a condition, that condition (manifest `when`). A
// name holding "<n>" is a template (overlayPartTemplated).
type overlayPart struct {
	Shape types.OverlayShape
	When  string
}

// overlayPartShapes is the per-kind table of declared payload parts —
// the single source of truth for both readers: the manifest
// (OverlayCapability.Parts, overlayPartsFor) lists every entry, and the
// Interpretation resolver admits `parts.<name>.cells.value` (MATRIX
// part), `parts.<name>.scalar` (SCALAR part) or
// `parts.<name>.summary.<key>` (SERIES part: each entry's summary key)
// only for a fixed-name entry (overlayPartShape). A kind without parts
// is absent.
var overlayPartShapes = map[types.OverlayKind]map[string]overlayPart{
	types.OverlayKindStdResidual:    stdResidualParts,
	types.OverlayKindCorrespondence: correspondenceParts,
	types.OverlayKindMarkov:         markovParts,
	types.OverlayKindRake:           rakeParts,

	types.OverlayKindMatrixGroupSummary: matrixGroupSummaryParts,
}

// overlayPartTemplated reports whether a declared part name is a
// template (step_<n>): it names a family of parts, not one key.
func overlayPartTemplated(name string) bool { return strings.Contains(name, "<") }

// overlayPartShape is the Interpretation resolver's view of
// overlayPartShapes: the shape of kind's fixed-name part, ok false for
// an undeclared or templated name.
func overlayPartShape(kind types.OverlayKind, name string) (types.OverlayShape, bool) {
	p, ok := overlayPartShapes[kind][name]
	if !ok || overlayPartTemplated(name) {
		return "", false
	}
	return p.Shape, true
}

// overlayPartsFor is the manifest's view of overlayPartShapes: kind's
// declared parts sorted by name, nil for a kind without parts.
func overlayPartsFor(kind types.OverlayKind) []descriptor.OverlayPart {
	decl := overlayPartShapes[kind]
	if len(decl) == 0 {
		return nil
	}
	out := make([]descriptor.OverlayPart, 0, len(decl))
	for name, p := range decl {
		out = append(out, descriptor.OverlayPart{Name: name, Shape: p.Shape, When: p.When})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// validateOverlayStdResidual enforces the per-kind contract for
// OVERLAY_STD_RESIDUAL — the OVERLAY_FISHER_EXACT_CELL contract: Ref
// must be EMPTY (implicit-margin: the residuals read the table's own row
// / column / grand totals), the host must be MATRIX-shaped
// (Request.Crosstab) and Scope must be CELL.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref arm is set
//     (overlayhost.CrosstabSpecRefusal, judged first).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything but CELL.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is nil.
func validateOverlayStdResidual(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, len(req.Groups) > 0, index)
		return
	}
}
