package service

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// MaterializeSink receives a materialize run. Begin is called once,
// after every pre-flight check passed and before any record is
// decoded, with the opened source cohort, the derived columns in
// pipeline order and the resolved output layout
// (descx.MaterializeOutputLayout: every column the cohort is written
// with, in output order, each reading a source field or a derived
// column); an error from it ends the run. Row is then called once
// per filter-passing source record — in source order, or in the
// request's sort order when it carries one — with the record's
// 0-based source index (global over a shard archive, in Cohort.Shards
// order — the CohortRecordReader index) and one value per derived
// column (float64 or nil). Row is never called when Begin failed.
type MaterializeSink interface {
	Begin(cohort *Cohort, columns []descx.MaterializeColumn, layout []descx.MaterializeOutputField) error
	Row(source int64, derived []any) error
}

// MaterializeWarner is a MaterializeSink that also receives the run's
// Response-level warnings — a fitted vector attribute's singular or
// insufficient fit — once, after the last Row.
type MaterializeWarner interface {
	Warn(warnings []*types.ResponseWarning)
}

// Materialize runs req's record-level pipeline — features, filters,
// attributes, windows — over its cohort and hands every filtered
// record's derived values to sink. It runs Process's own pre-flight in
// Process's order (cohort required, the instance slot gate, smart
// defaults, zones, the field-reference rule with weight resolution,
// the limits pre-flight) so a request Process refuses is refused with
// the same error, plus the materialize gates: a slot that does not
// produce one value per record (PULSE_MATERIALIZE_SLOT_UNSUPPORTED),
// no derived slot without an output block (PULSE_MATERIALIZE_NO_DERIVED),
// a feature whose outputs cannot be named
// (PULSE_MATERIALIZE_OUTPUT_UNDECLARED) and the output block's layout
// refusals (descx.MaterializeOutputLayout) — all before sink.Begin.
// Unlike every other operation it accepts the materialize-only
// `output` slot (descx.MaterializeSlotGate).
//
// A sort reorders the Row calls only (processing.DeriveRecords): the
// derived values are those of the unsorted run. The scan always
// decodes every field (no projection) and holds every record. The memory pre-flight already judges that buffered arm: with
// aggregations and matrices refused, no accepted request is
// streamable. Bound by Limits.RequestTimeout like Process.
func (s *Service) Materialize(ctx context.Context, req *types.Request, sink MaterializeSink) error {
	ctx, release := s.BoundRequest(ctx)
	defer release()
	err := s.materialize(ctx, req, sink)
	return s.scopeRefusal(MapRequestTimeout(ctx, err))
}

func (s *Service) materialize(ctx context.Context, req *types.Request, sink MaterializeSink) error {
	if req == nil || req.Cohort == nil {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "request cohort is required")
	}
	s = s.forRequest(req)
	// The instance slot gate with the materialize-only `output` slot
	// accepted (every other operation refuses it).
	if err := descx.MaterializeSlotGate(req, s.instance); err != nil {
		return markLocated(err)
	}
	// Every item for_each expands before the materialize gates read
	// the item lists, so every expanded item becomes a column.
	if err := s.expandItems(req); err != nil {
		return markLocated(err)
	}
	if err := descx.MaterializeSlotRefusal(req); err != nil {
		return err
	}
	// The request's `models`, loaded before any record is read (a
	// regression `save` never reaches here: regressions are refused
	// above, so a materialize run writes no model file).
	ctx, err := s.bindRequestModels(ctx, model.RequestRefs(req))
	if err != nil {
		return err
	}
	// Every comparison slot's {record} reference, resolved before any
	// processor builds (reference_records.go).
	ctx, err = s.bindReferenceRecords(ctx, req, req.Cohort)
	if err != nil {
		return err
	}
	if req.Output == nil && len(req.Features) == 0 && len(req.Attributes) == 0 && len(req.Windows) == 0 {
		_, err := descx.MaterializeColumns(req, nil, s.instance, "")
		return err
	}

	ei := execInfoFrom(ctx)
	ei.Lap(observe.PhasePlan)
	path := resolveCohortPath(req.Cohort)
	cohort, err := s.Open(ctx, path)
	if err != nil {
		return err
	}
	ei.Lap(observe.PhaseOpen)
	countReads(ctx, cohort)
	schema := cohort.Schema()

	s.applyDefaults(req, schema)
	zones, err := s.resolveZones(req, schema)
	if err != nil {
		return err
	}
	if err := s.checkFieldRefs(ctx, req, schema); err != nil {
		return err
	}
	lin, err := s.limitInputs(ctx, cohort, path)
	if err != nil {
		return err
	}
	if err := s.limitsPreflight(req, schema, lin); err != nil {
		return err
	}
	cols, cerr := descx.MaterializeColumns(req, schema, s.instance, path)
	if cerr != nil {
		return cerr
	}
	// The output layout (projection, renames, descriptions,
	// nullability) is resolved last, over the source schema plus the
	// derived columns: every slot above has already read the full
	// schema, so a column the output drops stays readable to them.
	layout, lerr := descx.MaterializeOutputLayout(req, schema, cols)
	if lerr != nil {
		return lerr
	}
	req = s.zoned(req, zones)
	ei.Lap(observe.PhasePlan)

	if err := sink.Begin(cohort, cols, layout); err != nil {
		return err
	}

	iter := s.newScanIter(cohort, path)
	defer iter.Close()
	pcols := make([]processing.DerivedColumn, len(cols))
	for i, c := range cols {
		pcols[i] = processing.DerivedColumn{Name: c.Name, Window: c.Window}
	}
	proc := s.newProcessor(ctx, schema, req)
	if err := proc.DeriveRecords(ctx, req, iter, pcols, sink.Row); err != nil {
		return err
	}
	if w, ok := sink.(MaterializeWarner); ok {
		w.Warn(proc.DeriveWarnings())
	}
	ei.setPlan(observe.ArmBuffered, 1, len(cohort.Shards()), 0)
	return iter.Err()
}
