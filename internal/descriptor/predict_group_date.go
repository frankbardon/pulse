package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/dategroup"
	"github.com/frankbardon/pulse/types"
)

// validateGroupDateParams reports every GROUP_DATE slot (Request.Groups
// and the crosstab axes) whose params or field the runtime factory
// refuses — through dategroup, the one reading the factory itself
// calls, so the code and message are the runtime's own. schema is the
// one the request executes over (the joined schema under a join).
func validateGroupDateParams(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) {
	if req == nil {
		return
	}
	check := func(g *types.Group) {
		if g == nil || opRoute(inst, g.Type) != types.GROUP_DATE {
			return
		}
		spec, err := dategroup.Parse(g.Params)
		if err == nil {
			err = dategroup.CheckField(spec, g.Field, schema)
		}
		if err != nil {
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
}
