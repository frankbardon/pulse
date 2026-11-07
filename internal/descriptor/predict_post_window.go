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
// instance route and extension hooks (an extension aggregator's
// FieldInputs), and cat the models predict resolved (nil: none, so an
// ATTR_REG_PREDICT reads nothing). Nil when req carries no window.
func classifyPostWindow(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, cat model.Catalog) *descriptor.PostWindowPlan {
	if req == nil || len(req.Windows) == 0 {
		return nil
	}
	cls := classifierOf(inst.Extensions(), cat)
	cls.Route = inst.attributeRoute()
	return postWindowDescriptor(req, schema, vectors.PostWindowAttributes(req, schema, cls))
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

// ClassifyPostWindow is the post-window plan Predict would report for
// req over a cohort whose schema is schema — without the cohort: the
// same pipeline up to the field-reference walk, the walk's own plan.
// A slot the instance hides or an item for_each refusal classifies
// req as written, schema-free (earlyPostWindow), as Predict does; else
// the items expand, the request's `models` / `populations` load
// through opts' ModelLoader, a join's right schema loads through
// opts' SchemaLoader (the joined schema, as Predict validates against
// it), defaults resolve over that schema unless opts disable them, and
// the walk classifies the result. A nil schema degrades to the
// schema-free classification of the expanded request (only a window
// label shadowing a schema field or vector is then misjudged). No
// refusal is reported: a request predict refuses still has a plan.
// Nil when the request carries no window.
func ClassifyPostWindow(req *types.Request, schema *encoding.Schema, opts *PredictOptions) *descriptor.PostWindowPlan {
	if req == nil {
		return nil
	}
	if opts == nil {
		opts = &PredictOptions{}
	}
	if SlotRefusal(req, opts.instance()) != nil {
		return earlyPostWindow(req, opts)
	}
	expanded, _, _, xerr := ExpandRequestItems(req, opts.instance().Limits(), opts.instance().SweepOptions())
	if xerr != nil {
		return earlyPostWindow(req, opts)
	}
	if schema == nil || len(expanded.Windows) == 0 {
		return earlyPostWindow(expanded, opts)
	}
	models, _, _ := predictModels(model.RequestRefs(expanded), opts.populations(), opts)
	if models != nil {
		opts = opts.withModels(models)
	}
	// The join refusals predict reports land on a scratch envelope: the
	// plan is classified over the schema predict would walk either way.
	schema = predictJoinedSchema(descriptor.NewEnvelope(nil), expanded, schema, opts)
	// No default rule moves the plan today (a defaulted slot reads its
	// field whatever type it infers); resolved anyway so the walk judges
	// exactly the request Predict's walk judges.
	resolved := cloneRequestForDefaults(expanded)
	if !opts.DisableDefaults {
		ResolveDefaults(resolved, schema, opts.Instance)
	}
	_, pw := fieldRefWalkPlan(resolved, schema, nil, extensionsFromOpts(opts), opts.Instance, models)
	return postWindowDescriptor(resolved, schema, pw)
}
