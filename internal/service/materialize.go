package service

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// MaterializeSink receives a materialize run. Begin is called once,
// after every pre-flight check passed and before any record is
// decoded, with the opened source cohort and the derived columns in
// output order; an error from it ends the run. Row is then called once
// per filter-passing source record, in source order, with the record's
// 0-based source index (global over a shard archive, in Cohort.Shards
// order — the CohortRecordReader index) and one value per derived
// column (float64 or nil). Row is never called when Begin failed.
type MaterializeSink interface {
	Begin(cohort *Cohort, columns []descx.MaterializeColumn) error
	Row(source int64, derived []any) error
}

// Materialize runs req's record-level pipeline — features, filters,
// attributes, windows — over its cohort and hands every filtered
// record's derived values to sink. It runs Process's own pre-flight in
// Process's order (cohort required, the instance slot gate, smart
// defaults, zones, the field-reference rule with weight resolution,
// the limits pre-flight) so a request Process refuses is refused with
// the same error, plus the materialize gates: a slot that does not
// produce one value per record (PULSE_MATERIALIZE_SLOT_UNSUPPORTED),
// no derived slot (PULSE_MATERIALIZE_NO_DERIVED) and a feature whose
// outputs cannot be named (PULSE_MATERIALIZE_OUTPUT_UNDECLARED) — all
// before sink.Begin.
//
// The scan always decodes every field (no projection) and holds every
// record. The memory pre-flight already judges that buffered arm: with
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
	if err := s.slotRefusal(req); err != nil {
		return markLocated(err)
	}
	if err := descx.MaterializeSlotRefusal(req); err != nil {
		return err
	}
	if len(req.Features) == 0 && len(req.Attributes) == 0 && len(req.Windows) == 0 {
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
	if err := s.checkFieldRefs(req, schema); err != nil {
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
	req = s.zoned(req, zones)
	ei.Lap(observe.PhasePlan)

	if err := sink.Begin(cohort, cols); err != nil {
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
	ei.setPlan(observe.ArmBuffered, 1, len(cohort.Shards()), 0)
	return iter.Err()
}
