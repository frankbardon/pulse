package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/dategroup"
	"github.com/frankbardon/pulse/internal/datepart"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// validateGroupDateParams reports every GROUP_DATE slot (Request.Groups,
// the crosstab axes and a MAT_DISTANCE params.between) whose params or
// field the runtime factory refuses — through dategroup and the
// accepted-types table, the readings the factory itself calls in its
// order, so the code and message are the runtime's own. schema is the
// one the request executes over (the joined schema under a join).
func validateGroupDateParams(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) {
	if req == nil {
		return
	}
	check := func(g *types.Group) {
		if g == nil || opRoute(inst, g.Type) != types.GROUP_DATE {
			return
		}
		// The factory's order: params, then the accepted-types table's
		// date-family row, then the sub-day datetime rule.
		spec, err := dategroup.Parse(g.Params)
		if err != nil {
			addCodedError(env, err)
			return
		}
		if cerr := fieldaccept.Check(string(types.GROUP_DATE), fieldaccept.SlotField, g.Field, schema); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			return
		}
		// A vector field is the field-reference walk's
		// PULSE_VECTOR_FIELD_NOT_SCALAR alone (the runtime refuses it
		// before the factory runs).
		if schema != nil {
			if f := schema.Field(g.Field); f != nil && f.Type.IsVector() {
				return
			}
		}
		if err := dategroup.CheckField(spec, g.Field, schema); err != nil {
			addCodedError(env, err)
		}
	}
	for _, g := range req.Groups {
		check(g)
	}
	if ct := req.Crosstab; ct != nil {
		for _, g := range ct.Rows {
			check(g)
		}
		for _, g := range ct.Columns {
			check(g)
		}
	}
	// A MAT_DISTANCE params.between GROUP_DATE is built by the same
	// factory (internal/processing newDistanceState).
	for _, m := range req.Matrices {
		if opRoute(inst, m.Type) != types.MAT_DISTANCE {
			continue
		}
		if g, ok := vectors.SpecBetween(m); ok {
			check(&g)
		}
	}
}

// validateDatePartParams reports every ATTR_DATE_PART slot whose params
// or schema field the runtime factory refuses — through datepart, the
// one reading the factory itself calls, so the code and message are the
// runtime's own. A field absent from schema is left to the
// field-reference rule (FieldRefRefusals), as before.
func validateDatePartParams(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) {
	if req == nil {
		return
	}
	for _, a := range req.Attributes {
		if a == nil || opRoute(inst, a.Type) != types.ATTR_DATE_PART {
			continue
		}
		part, err := datepart.Parse(a.Params)
		if err == nil && schema != nil {
			if f := schema.Field(a.Field); f != nil && !f.Type.IsVector() {
				err = datepart.CheckField(part, a.Field, f)
			}
		}
		if err != nil {
			addCodedError(env, err)
		}
	}
}
