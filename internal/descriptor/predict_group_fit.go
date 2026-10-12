package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/groupfit"
	"github.com/frankbardon/pulse/types"
)

// validateGroupFitParams reports every data-dependent grouper slot
// (GROUP_QUANTILE / GROUP_KMEANS on Request.Groups and both crosstab
// axes) whose params.fit the runtime grouper factory refuses — through
// internal/groupfit, the one reading the factories call, so the code,
// message and details are the runtime's own. "within" on the outermost
// position is accepted (a no-op: no outer segment). fit never moves the
// fused-crosstab answer: neither grouper keys one record at a time.
func validateGroupFitParams(env *descriptor.Envelope, req *types.Request, inst *InstanceSnapshot) {
	if req == nil {
		return
	}
	check := func(g *types.Group) {
		if g == nil || !groupfit.Applies(opRoute(inst, g.Type)) {
			return
		}
		if _, err := groupfit.Parse(g.Type, g.Params); err != nil {
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
