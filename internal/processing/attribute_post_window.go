package processing

import (
	"context"

	"github.com/frankbardon/pulse/internal/prevstate"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// The post-window attribute phase (transformed-predictors E2-S1).
//
// Attributes run over the filtered records before the windows. A
// row-local attribute that reads a window label — directly, or through
// an earlier post-window attribute (vectors.PostWindowAttributes, the
// classification the field-reference walk judges by too) — instead runs
// AFTER the windows when they run over the record rows: the window
// labels it reads are copied onto the records exactly as the
// materialize route stores them (copyWindowMembers → derivedFloat), then
// the post-window slots run over the same filtered records in declared
// order, writing their outputs under their labels like any attribute —
// on the record (so a regression, a matrix or a later post-window slot
// reads them) and on the record's output row (so the response row, a
// sort key and a post-test see them). A request whose attributes read no
// window label has an empty plan and takes the historical path
// unchanged.

// attrSlotOut is one attribute slot's run: its warnings and its
// Components.Attributes entry (ok false when it emits none).
type attrSlotOut struct {
	warns []*types.ResponseWarning
	comp  types.AttributeComponents
	ok    bool
}

// postWindowPlan classifies req's attributes over the processor's bound
// models and refuses a window-dependent slot that cannot move
// (PULSE_ATTRIBUTE_WINDOW_ORDER — the field-reference walk's refusal,
// re-raised for a processor driven without the service). Its
// WindowsFirst reports the windows-first mode (vectors.WindowsFirst,
// with the bound models): every shape runs it (windowsFirstPass) —
// ungrouped and grouped in processRecords, the buffered crosstab in
// RunCrosstab; the streaming arms and the fused crosstab never see it
// (canStream and the fusion gate refuse any window).
func (p *Processor) postWindowPlan(req *types.Request) (vectors.PostWindow, error) {
	pw := vectors.PostWindowAttributes(req, p.schema, nil, p.catalog())
	if cerr := pw.Refusal(req); cerr != nil {
		return vectors.PostWindow{}, cerr
	}
	return pw, nil
}

// windowsFirstPass is the windows-first pre-step every shape runs after
// the filter and pre-window attribute pass and BEFORE its groups,
// aggregations or crosstab cells: the windows run over scratch record
// rows of filtered (no key orders — windows over records, partition_by
// unchanged), every window label an output slot, matrix, regression or
// post-window attribute reads is copied onto filtered[i]
// (copyWindowMembers), then the post-window attributes run over the
// records (nil rows — the record carries the label), filling outs. The
// scratch rows are dropped: the response rows are the group,
// aggregation or crosstab rows.
func (p *Processor) windowsFirstPass(ctx context.Context, req *types.Request, post vectors.PostWindow, filtered []*Record, outs []attrSlotOut) error {
	rows, words, err := recordsToRows(filtered, p.schema)
	if err != nil {
		return err
	}
	if err := p.applyWindows(ctx, rows, req.Windows, nil, words.copyObserver()); err != nil {
		return err
	}
	if err := p.copyWindowMembers(req, post, filtered, rows); err != nil {
		return err
	}
	if post.Any() {
		return p.applyPostWindowAttributes(req, post, filtered, nil, outs)
	}
	return nil
}

// filterThenAttributeSlots is filterThenAttributes with the post-window
// slots (post) left for applyPostWindowAttributes: it returns the
// filter-passing records, the per-filterer counters and one output per
// attribute slot (a post-window slot's still empty).
func (p *Processor) filterThenAttributeSlots(req *types.Request, records []*Record, post vectors.PostWindow) ([]*Record, []filterPassCounters, []attrSlotOut, error) {
	outs := make([]attrSlotOut, len(req.Attributes))
	for i, attr := range req.Attributes {
		if attr == nil || !prevstate.BeforeFilters(attr.Type) {
			continue
		}
		w, entry, ok, err := p.applyAttribute(attr, records)
		if err != nil {
			return nil, nil, nil, err
		}
		outs[i] = attrSlotOut{warns: w, comp: entry, ok: ok}
	}
	filtered, counters, err := p.applyFiltersWithCounters(req.Filterers, records)
	if err != nil {
		return nil, nil, nil, err
	}
	for i, attr := range req.Attributes {
		if (attr != nil && prevstate.BeforeFilters(attr.Type)) || post.IsPost(i) {
			continue
		}
		w, entry, ok, err := p.applyAttribute(attr, filtered)
		if err != nil {
			return nil, nil, nil, err
		}
		outs[i] = attrSlotOut{warns: w, comp: entry, ok: ok}
	}
	return filtered, counters, outs, nil
}

// applyPostWindowAttributes runs the post-window slots, in declared
// order, over records (the filtered set, the window labels they read
// already copied on), filling outs[i]; rows[j] (records[j]'s output
// row, nil when none) receives each slot's value under its label — a
// null output leaves the cell absent, exactly as recordsToRows renders
// an earlier attribute's null.
func (p *Processor) applyPostWindowAttributes(req *types.Request, post vectors.PostWindow, records []*Record, rows []map[string]any, outs []attrSlotOut) error {
	for i, attr := range req.Attributes {
		if !post.IsPost(i) {
			continue
		}
		w, entry, ok, err := p.applyAttribute(attr, records)
		if err != nil {
			return err
		}
		outs[i] = attrSlotOut{warns: w, comp: entry, ok: ok}
		if rows == nil {
			continue
		}
		label := attr.Label
		if label == "" {
			label = defaultAttributeLabel(attr)
		}
		for j, rec := range records {
			if v, ok := rec.NumericValue(label); ok {
				rows[j][label] = v
			}
		}
	}
	return nil
}

// mergeAttrSlots flattens per-slot outputs into the declared-order
// warnings and Components.Attributes entries.
func mergeAttrSlots(outs []attrSlotOut) ([]*types.ResponseWarning, []types.AttributeComponents) {
	var warns []*types.ResponseWarning
	var comps []types.AttributeComponents
	for _, o := range outs {
		warns = append(warns, o.warns...)
		if o.ok {
			comps = append(comps, o.comp)
		}
	}
	return warns, comps
}
