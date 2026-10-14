package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// validateFieldAcceptance reports every aggregation, attribute, group
// and filterer slot whose schema field the accepted-types table
// (internal/fieldaccept) refuses for its operator — the type check the
// runtime constructor makes, with the runtime's code, once per slot.
// Windows, features, regressions and tests consult the table in their
// own validators.
//
// The lookup keys on the operator's route, so a profile-hidden operator
// predicts as never registered (the table is never consulted for it).
// A field the schema does not carry (an unknown name, a derived column)
// is left to the field-reference walk; a vector field to its
// PULSE_VECTOR_FIELD_NOT_SCALAR refusal.
func validateFieldAcceptance(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) {
	if req == nil || schema == nil {
		return
	}
	check := func(op string, slot fieldaccept.Slot, field string) {
		if op == "" {
			return
		}
		if cerr := fieldaccept.Check(op, slot, field, schema); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
	}
	agg := func(a *types.Aggregation) {
		if a != nil {
			check(string(opRoute(inst, a.Type)), fieldaccept.SlotField, a.Field)
		}
	}
	group := func(g *types.Group) {
		if g != nil {
			check(string(opRoute(inst, g.Type)), fieldaccept.SlotField, g.Field)
		}
	}
	for _, a := range req.Aggregations {
		agg(a)
	}
	for _, a := range req.Attributes {
		if a == nil {
			continue
		}
		op := string(opRoute(inst, a.Type))
		check(op, fieldaccept.SlotField, a.Field)
		check(op, fieldaccept.SlotTarget, a.Target)
		for _, p := range a.Predictors {
			check(op, fieldaccept.SlotPredictor, p)
		}
	}
	for _, g := range req.Groups {
		group(g)
	}
	if ct := req.Crosstab; ct != nil {
		for _, g := range ct.Rows {
			group(g)
		}
		for _, g := range ct.Columns {
			group(g)
		}
		agg(ct.Cell)
		for _, a := range ct.MarginAggregations {
			agg(a)
		}
	}
	for _, f := range req.Filterers {
		if f == nil || !filterTypeChecked(opRoute(inst, f.Type), f.Values) {
			continue
		}
		check(string(opRoute(inst, f.Type)), fieldaccept.SlotField, f.Field)
	}
}

// filterTypeChecked reports whether the table judges a filterer's field
// type. FILTER_TRUE / FILTER_FALSE read only a packed_bool in strict
// mode (no values, or ["strict"]); ["truthy"] coerces any scalar, and
// any other values list is the runtime's mode refusal, raised before the
// type is read.
func filterTypeChecked(t types.FiltererType, values []string) bool {
	if t != types.FILTER_TRUE && t != types.FILTER_FALSE {
		return true
	}
	return len(values) == 0 || (len(values) == 1 && values[0] == "strict")
}
