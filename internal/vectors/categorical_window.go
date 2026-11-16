package vectors

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// CategoricalWindowRefusal is the one judgement of who may read a
// categorical WIN_LAG / WIN_LEAD output (windowparams.CategoricalLags —
// a lagged category, mmm-optimize-depth E4-S1), shared by the
// field-reference walk (predict and the service entry check) and the
// engine (Processor.applyWindows), so both refuse the same request with
// the same code and details. The output is a category on its source's
// dictionary; it is read:
//
//   - by a later window's field or WIN_LIFT base through the accepted-types
//     table at the source's categorical type: another WIN_LAG / WIN_LEAD
//     copies it (a lag of a lag), every value window refuses it;
//   - windows over the records (WindowsOverRecords), as a GROUP_CATEGORY
//     key — a request group or a crosstab axis — only. Every other
//     record-column read (an aggregation's field or column param, any
//     other grouper, a regression, a MAT_* member, a post-window
//     attribute) would read a dictionary index as a number, so it is
//     refused.
//
// Refusals are PULSE_FIELD_TYPE_UNSUPPORTED with details {type, slot,
// field, field_type} (+ window_index, the producing window, and
// reason "categorical_window_output" for a record-column read). A
// partition_by / order_by key, a WIN_FORMULA's expression and the
// record-row output read the label itself and are never refused here.
// Nil when req has no categorical lag (one pass over its windows) or
// nothing misreads one.
func CategoricalWindowRefusal(req *types.Request, schema *encoding.Schema, c Classifier) *errors.CodedError {
	if req == nil || len(req.Windows) == 0 {
		return nil
	}
	cats := windowparams.CategoricalLags(req.Windows, schema)
	if len(cats) == 0 {
		return nil
	}
	// A later window reading an earlier categorical lag's label.
	for j, w := range req.Windows {
		if w == nil {
			continue
		}
		if l, ok := cats[w.Field]; ok && l.Index < j {
			if cerr := fieldaccept.Refusal(string(w.Type), fieldaccept.SlotField, w.Field, l.Field.Type); cerr != nil {
				return cerr
			}
		}
		if w.Type == types.WIN_LIFT {
			if base := liftBase(w); base != "" {
				if l, ok := cats[base]; ok && l.Index < j {
					if cerr := fieldaccept.Refusal(string(w.Type), fieldaccept.SlotBase, base, l.Field.Type); cerr != nil {
						return cerr
					}
				}
			}
		}
	}
	if !WindowsOverRecords(req, schema, c) {
		return nil
	}
	refuse := func(op, slot, label string) *errors.CodedError {
		l := cats[label]
		return errors.NewCodedErrorWithDetails(errors.PULSE_FIELD_TYPE_UNSUPPORTED,
			op+" "+slot+" "+label+" is a lagged category ("+string(req.Windows[l.Index].Type)+" of "+l.Source+", "+l.Field.Type.String()+
				"): it is read only as a GROUP_CATEGORY key (a request group or a crosstab axis); materialize it to aggregate it as a stored categorical",
			map[string]any{"type": op, "slot": slot, "field": label, "field_type": l.Field.Type.String(),
				"window_index": l.Index, "reason": "categorical_window_output"})
	}
	aggs := append([]*types.Aggregation(nil), req.Aggregations...)
	var axes []*types.Group
	axes = append(axes, req.Groups...)
	if spec := req.Crosstab; spec != nil {
		axes = append(append(axes, spec.Rows...), spec.Columns...)
		aggs = append(append(aggs, spec.CellAggregations()...), spec.MarginAggregations...)
	}
	for _, a := range aggs {
		for _, n := range AggregationColumnReads(a, c.Ext) {
			if _, ok := cats[n]; ok {
				return refuse(string(a.Type), "field", n)
			}
		}
	}
	for _, g := range axes {
		for _, n := range GroupColumnReads(g, c.Ext) {
			if _, ok := cats[n]; ok && (g.Type != types.GROUP_CATEGORY || n != g.Field) {
				return refuse(string(g.Type), "field", n)
			}
		}
	}
	for _, n := range RegressionWindowReads(req, schema, c) {
		if _, ok := cats[n]; ok {
			return refuse("regression", "input", n)
		}
	}
	for _, n := range MatrixComputedMembers(req, schema, c) {
		if _, ok := cats[n]; ok {
			return refuse("matrix", "member", n)
		}
	}
	pw := PostWindowAttributes(req, schema, c)
	k := 0
	for i, attr := range req.Attributes {
		if !pw.IsPost(i) {
			continue
		}
		if k < len(pw.PostReads) {
			for _, n := range pw.PostReads[k] {
				if _, ok := cats[n]; ok {
					return refuse(string(attr.Type), "input", n)
				}
			}
		}
		k++
	}
	return nil
}

// liftBase is a WIN_LIFT slot's params.base, "" when absent or
// malformed (the shared reading's refusal).
func liftBase(w *types.Window) string {
	var p struct {
		Base string `json:"base"`
	}
	if len(w.Params) == 0 || paramread.Probe(w.Params, &p) != nil {
		return ""
	}
	return p.Base
}
