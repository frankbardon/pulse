package guide

import (
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// explain_operator.go is Explain's operator mode (U64 E1-S2): one
// operator's validation evidence — the external references its figures
// are pinned to, a stated gap, a pinned weight refusal and the NIST
// StRD digits — pulled on request from the registry the book renders
// (descx.InstanceSnapshot.Validation), so the two cannot disagree. It
// reads no cohort and runs nothing.

// explainOperator fills the result for the operator op. An operator
// the instance does not offer — unknown or hidden alike — is
// SERVICE_VALIDATION, so a hidden name acts never-registered.
func (e *explainer) explainOperator(op string) error {
	if !e.g.Has(descx.OntologyID(descriptor.OntologyNodeOperator, op)) {
		return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"explain: operator "+op+" is not an operator this instance offers",
			map[string]any{"field": "operator", "operator": op})
	}
	e.noteOp(op, true)
	v, ok := e.inst.Validation(op)
	if !ok {
		e.res.Summary = "Pulse holds no validation record for " + op + "."
		e.caveat("Only the built-in tests, regressions, matrix operators, inferential overlays and the operators a certified dataset checks carry a validation record, so nothing here says how " + op + " was checked.")
		return nil
	}
	e.res.Validation = &v
	switch n := len(v.References); n {
	case 0:
		e.res.Summary = op + " has no external reference."
	case 1:
		e.res.Summary = op + " is checked against 1 external reference."
	default:
		e.res.Summary = op + " is checked against " + strconv.Itoa(n) + " external references."
	}
	if len(v.Certification) > 0 {
		e.notes = append(e.notes, "It runs on "+count(len(v.Certification), "NIST certified dataset", "NIST certified datasets")+"; each figure's digits of agreement are listed per dataset.")
	}
	if v.Gap != "" {
		e.caveat(op + " has a stated validation gap; read it in validation.gap before relying on the figures it names.")
	}
	if v.WeightRefusal != nil {
		e.caveat("A weight on " + op + " is refused, not validated: " + op + " has no weighted form.")
	}
	return nil
}
