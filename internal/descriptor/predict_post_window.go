package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// postWindowDescriptor is PredictResult.PostWindow (and a Compose
// slot's ComposeSlotPredict.PostWindow): pw — the plan the
// field-reference walk classified req by over schema (fieldRefWalkPlan),
// never a second classification — as the wire block. Nil when req
// carries no window. Attribute indices address req's attributes (the
// request the walk judged: item for_each expanded).
func postWindowDescriptor(req *types.Request, schema *encoding.Schema, pw vectors.PostWindow) *descriptor.PostWindowPlan {
	if req == nil || len(req.Windows) == 0 {
		return nil
	}
	out := &descriptor.PostWindowPlan{
		WindowsFirst: pw.WindowsFirst,
		WindowLabels: vectors.WindowDerivedLabels(req, schema),
		Attributes:   []descriptor.PostWindowAttribute{},
	}
	if out.WindowLabels == nil {
		out.WindowLabels = []string{}
	}
	k := 0
	for i := range req.Attributes {
		if !pw.IsPost(i) {
			continue
		}
		entry := descriptor.PostWindowAttribute{Index: i, DependsOn: pw.DepOf(i), WindowReads: []string{}}
		if k < len(pw.Labels) {
			entry.Label = pw.Labels[k]
		}
		if k < len(pw.PostReads) && len(pw.PostReads[k]) > 0 {
			entry.WindowReads = append(entry.WindowReads, pw.PostReads[k]...)
		}
		out.Attributes = append(out.Attributes, entry)
		k++
	}
	return out
}

// classifyPostWindow is the post-window block of a request predict
// judges WITHOUT reaching the field-reference walk — a Process request
// refused before it (a hidden slot, an item for_each refusal, an
// unreadable cohort header or schema) or a Compose slot refused before
// its field references (join count, matrix host, `return`, join keys,
// zones), or one with no schema to walk against. The same classifier
// call the walk makes (vectors.PostWindowAttributes), over whatever is
// known: schema nil when unread (only a window label shadowing a field
// is then misjudged — a request the walk would refuse anyway), the
// instance route, and cat the models predict resolved (nil: none, so an
// ATTR_REG_PREDICT reads nothing). Nil when req carries no window.
func classifyPostWindow(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, cat model.Catalog) *descriptor.PostWindowPlan {
	if req == nil || len(req.Windows) == 0 {
		return nil
	}
	route := func(t types.AttributeType) types.AttributeType { return opRoute(inst, t) }
	return postWindowDescriptor(req, schema, vectors.PostWindowAttributes(req, schema, route, cat))
}

// earlyPostWindow is classifyPostWindow for a Process predict stopped
// before its field-reference walk: no schema, the models the request's
// `models` / `populations` resolve to through opts (a load failure
// leaves the catalog it could build — the walk would report it).
func earlyPostWindow(req *types.Request, opts *PredictOptions) *descriptor.PostWindowPlan {
	if req == nil || len(req.Windows) == 0 {
		return nil
	}
	if opts == nil {
		opts = &PredictOptions{}
	}
	cat, _, _ := predictModels(model.RequestRefs(req), opts.populations(), opts)
	return classifyPostWindow(req, nil, opts.instance(), cat)
}
