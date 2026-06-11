package processing

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// RunCrosstabFused executes a Request.Crosstab against a streaming
// record iterator using the fused accumulator path. The caller is
// responsible for dispatching through CanFuseCrosstab before invoking
// this method; AssertCanFuse echoes the gate's exclusions so a mis-
// routed request fails fast with a typed CodedError rather than
// producing a divergent fused result.
//
// Pipeline mirrors the buffered RunCrosstab pre-aggregate order — there
// are no features and no two-pass attributes by gate construction, so
// the surviving stages reduce to filters → row-local attributes →
// per-cell fold. Both helpers run on the same per-record cursor
// (filters first, then attributes) so a label injected by an attribute
// is visible to the cell aggregator on the next iteration but never
// participates in the filter chain — same ordering the buffered
// processCrosstab path establishes via applyFilters then
// applyAttributes.
//
// Total-row counting advances on every iterator record regardless of
// whether the filter chain passes; filtered-row counting advances
// inside FusedCrosstabState.Update via state.filteredRows. The two
// counts feed Response.Metadata.TotalRows / FilteredRows respectively.
//
// Iterator reuse is opted in via EnableReuse — the fused state never
// retains a record reference past the Update call, so per-row Record
// reuse is safe and shaves the per-record allocation cost on wide
// cohorts.
//
// Errors surface the standard CodedError shapes. A gate drift that
// allows a non-streamable grouper / non-online cell aggregator to reach
// here surfaces as PROCESSING_INTERNAL via NewFusedCrosstabState or
// AssertCanFuse.
func (p *Processor) RunCrosstabFused(_ context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	if req == nil || req.Crosstab == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"RunCrosstabFused requires a Crosstab spec")
	}
	if iter == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"RunCrosstabFused requires a non-nil record iterator")
	}

	spec := req.Crosstab
	if err := validateCrosstabSpec(spec, req); err != nil {
		return nil, err
	}

	state, err := NewFusedCrosstabState(spec, p.schema, p.exts)
	if err != nil {
		return nil, err
	}
	// Defensive: echo the static gate's exclusions so a stale dispatch
	// shortcut that drifts past a newly added request slot fails fast
	// here rather than producing a divergent fused result.
	if err := state.AssertCanFuse(req); err != nil {
		return nil, err
	}

	filterFns, err := p.buildFilterFuncs(req.Filterers)
	if err != nil {
		return nil, err
	}
	rowLocalAttrs, err := p.buildRowLocalAttributes(req.Attributes)
	if err != nil {
		return nil, err
	}

	// NOTE: We intentionally do NOT call EnableReuse(iter) here, even
	// though the FusedCrosstabState consumes each record inline and
	// retains no pointer past Update. The streamingIterator's reuse
	// fast path (ReadRecordReused) walks every schema field — it does
	// NOT honour the projection installed by
	// service.applyCrosstabProjection. On wide cohorts (the 200-field
	// canonical bench) skipping projection costs an order of magnitude
	// more than the per-record map allocation the non-reuse path
	// incurs. The non-reuse Next path routes through
	// ReadRecordWithWidePlan whenever a DecodePlan was installed, so
	// the per-record decode budget collapses to the retained subset.
	// Plumbing a projected-reuse decoder is the long-term cleanup; the
	// trade-off here favours the dramatically larger projection win.
	for iter.Next() {
		rec := iter.Record()
		state.AddTotalRow()

		pass := true
		for _, fn := range filterFns {
			ok, ferr := fn(rec)
			if ferr != nil {
				return nil, ferr
			}
			if !ok {
				pass = false
				break
			}
		}
		if !pass {
			continue
		}

		// Apply row-local attributes inline; values land on the record
		// under their label so cell aggregations referencing the label
		// resolve the same way the buffered RunCrosstab path resolves
		// them via applyAttributes.
		for _, ra := range rowLocalAttrs {
			val, rerr := ra.computer.Row(rec, ra.attr.Field)
			if rerr != nil {
				return nil, rerr
			}
			rec.Set(ra.label, val)
		}

		if uerr := state.Update(rec); uerr != nil {
			return nil, uerr
		}
	}

	return state.Finalize()
}
