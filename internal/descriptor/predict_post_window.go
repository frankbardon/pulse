package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
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
