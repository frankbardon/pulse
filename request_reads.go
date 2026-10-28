package pulse

import (
	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// RequestReads returns a request document's read map: for every
// component slot that reads data — an expression, a field slot, an
// operator param that names a column — the component's JSON path, the
// key inside it, and the columns and request parameters it reads
// ([descriptor.SlotReads]). It is no-execute, like Predict without a
// cohort: no compile, no engine run, no cohort I/O. An editor drawing
// each component's wires to the columns and parameters it depends on,
// a linter checking a draft against a schema, or an agent planning the
// columns an analysis needs reads this instead of re-implementing
// Pulse's slot list and expression scoping.
//
// doc is the raw JSON document of a Request, ComposedRequest,
// ChainRequest or FacetRequest; root names which (""
// detects it from the top-level keys: stages is a chain; requests,
// sweep, banner, fits or optimize a compose; fields a facet; otherwise
// a request). Compose sub-requests (requests, fits, the sweep and
// banner requests, banner sections), the compose optimize blocks
// (whose names are decision Variables, never columns, and whose
// `"params.<name>"` bounds are params reads) and chain stages are all
// walked; each slot's Request names the request whose params it
// resolves against, beneath the root's inherited `params`.
//
// The slots read are Pulse's own: the expression slots the template
// renderer and the engine share, the instance manifest's params marked
// names_column, and an extension operator's FieldInputs hook (an
// extension without one contributes only its typed slots). An operator
// the instance does not know contributes only its typed slots.
//
// The map is taken over the document as written: it does not need to
// validate. A slot that cannot be read — an expression that does not
// parse, a value of the wrong JSON shape — carries its own Error and
// the rest of the map is returned; only a doc that is not a JSON object
// or an unknown root is a SERVICE_VALIDATION error. Render request
// templates first: an unrendered `{{token}}` in an expression slot is
// that slot's PULSE_FORMULA_COMPILE_ERROR, and elsewhere reads as the
// literal name.
func (p *Pulse) RequestReads(doc []byte, root descriptor.ReadsRoot) (descriptor.RequestReads, error) {
	out, ce := descx.RequestReads(doc, root, p.svc.InstanceSnapshot())
	if ce != nil {
		return out, ce
	}
	return out, nil
}
