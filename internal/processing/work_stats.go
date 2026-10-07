package processing

import "sync/atomic"

// Process-wide work counters (see WorkStats). Each is touched once per
// request, slot or bucket at a BUILD point — never in a per-record
// loop — so they cost nothing on the hot path. Tests read them as
// deltas to prove an excluded part was never computed
// (TestReturnSkipsComputation).
var (
	workAggComponentBuilds      atomic.Int64
	workGroupFloorBuilds        atomic.Int64
	workGrouperComponentBuilds  atomic.Int64
	workFiltererComponentBuilds atomic.Int64
	workRunComponentBuilds      atomic.Int64
	workMatrixComponentBuilds   atomic.Int64
	workCrosstabComponentMaps   atomic.Int64
	workAuxMarginAccumulators   atomic.Int64
	workMatrixAccumulators      atomic.Int64
	workMatrixResultBuilds      atomic.Int64
	workMatrixAuxiliaryBuilds   atomic.Int64
	workMatrixScalarBuilds      atomic.Int64
	workMatrixVectorBuilds      atomic.Int64
	workOverlayLayerRuns        atomic.Int64
)

// WorkStatsSnapshot is a snapshot of the process-wide work counters.
// Every field is a monotonic total since process start.
type WorkStatsSnapshot struct {
	// AggComponentBuilds counts Components.Aggregations entries built:
	// one per slot (ungrouped, or a grouped run's cohort-wide floor)
	// and one per (bucket, slot) on a grouped run.
	AggComponentBuilds int64
	// GroupFloorBuilds counts per-group aggregation floor work: each
	// per-bucket floor table allocated (a serial grouped scan, a
	// parallel worker, a shard) and each groups[] assembly.
	GroupFloorBuilds int64
	// GrouperComponentBuilds counts Components.Groupers entries built.
	GrouperComponentBuilds int64
	// FiltererComponentBuilds counts Components.Filterers blocks built.
	FiltererComponentBuilds int64
	// RunComponentBuilds counts Components.Run blocks built.
	RunComponentBuilds int64
	// MatrixComponentBuilds counts Components.Matrices entries built.
	MatrixComponentBuilds int64
	// CrosstabCellComponentMaps counts crosstab component maps built
	// (buildCellComponentMap): one per occupied cell, per row / column
	// margin slot, the grand margin, and per auxiliary margin figure.
	// Every one lands under components.crosstab.
	CrosstabCellComponentMaps int64
	// AuxMarginAccumulators counts auxiliary margin aggregator
	// instances run (crosstab.margin_aggregations): one per (auxiliary,
	// margin slot) the buffered arm evaluates over a non-empty admitted
	// set, and one per (auxiliary, slot) the fused arm constructs on a
	// slot's first admitted record.
	AuxMarginAccumulators int64
	// MatrixAccumulators counts Request.Matrices slots minted — the
	// per-record co-moment fold: one per spec on an ungrouped run (per
	// partition on the parallel / shard arms, one more for the
	// merge-target), one per (bucket, spec) on a grouped run.
	MatrixAccumulators int64
	// MatrixResultBuilds counts MatrixResults rendered at finalize (the
	// primary matrix and its warnings): one per (spec, bucket). A run
	// that keeps only components.matrices folds but renders none.
	MatrixResultBuilds int64
	// MatrixAuxiliaryBuilds / MatrixScalarBuilds / MatrixVectorBuilds
	// count the finalize-only matrices[*].auxiliary (a pairwise slot's
	// per-pair n), .scalars (the Cholesky determinant) and .vectors
	// (top_pairs) renders: one per rendered MatrixResult carrying the
	// sub-part.
	MatrixAuxiliaryBuilds int64
	MatrixScalarBuilds    int64
	MatrixVectorBuilds    int64
	// OverlayLayerRuns counts overlay layer folds: one per spec a
	// request-host (crosstab or series) or Compose-host dispatch loop
	// runs through its handler. A layer the ComputePlan skips counts
	// nothing.
	OverlayLayerRuns int64
}

// WorkStats returns the process-wide work counters. Diagnostic only;
// tests read deltas.
func WorkStats() WorkStatsSnapshot {
	return WorkStatsSnapshot{
		AggComponentBuilds:        workAggComponentBuilds.Load(),
		GroupFloorBuilds:          workGroupFloorBuilds.Load(),
		GrouperComponentBuilds:    workGrouperComponentBuilds.Load(),
		FiltererComponentBuilds:   workFiltererComponentBuilds.Load(),
		RunComponentBuilds:        workRunComponentBuilds.Load(),
		MatrixComponentBuilds:     workMatrixComponentBuilds.Load(),
		CrosstabCellComponentMaps: workCrosstabComponentMaps.Load(),
		AuxMarginAccumulators:     workAuxMarginAccumulators.Load(),
		MatrixAccumulators:        workMatrixAccumulators.Load(),
		MatrixResultBuilds:        workMatrixResultBuilds.Load(),
		MatrixAuxiliaryBuilds:     workMatrixAuxiliaryBuilds.Load(),
		MatrixScalarBuilds:        workMatrixScalarBuilds.Load(),
		MatrixVectorBuilds:        workMatrixVectorBuilds.Load(),
		OverlayLayerRuns:          workOverlayLayerRuns.Load(),
	}
}

// Sub is the per-counter delta s - o.
func (s WorkStatsSnapshot) Sub(o WorkStatsSnapshot) WorkStatsSnapshot {
	return WorkStatsSnapshot{
		AggComponentBuilds:        s.AggComponentBuilds - o.AggComponentBuilds,
		GroupFloorBuilds:          s.GroupFloorBuilds - o.GroupFloorBuilds,
		GrouperComponentBuilds:    s.GrouperComponentBuilds - o.GrouperComponentBuilds,
		FiltererComponentBuilds:   s.FiltererComponentBuilds - o.FiltererComponentBuilds,
		RunComponentBuilds:        s.RunComponentBuilds - o.RunComponentBuilds,
		MatrixComponentBuilds:     s.MatrixComponentBuilds - o.MatrixComponentBuilds,
		CrosstabCellComponentMaps: s.CrosstabCellComponentMaps - o.CrosstabCellComponentMaps,
		AuxMarginAccumulators:     s.AuxMarginAccumulators - o.AuxMarginAccumulators,
		MatrixAccumulators:        s.MatrixAccumulators - o.MatrixAccumulators,
		MatrixResultBuilds:        s.MatrixResultBuilds - o.MatrixResultBuilds,
		MatrixAuxiliaryBuilds:     s.MatrixAuxiliaryBuilds - o.MatrixAuxiliaryBuilds,
		MatrixScalarBuilds:        s.MatrixScalarBuilds - o.MatrixScalarBuilds,
		MatrixVectorBuilds:        s.MatrixVectorBuilds - o.MatrixVectorBuilds,
		OverlayLayerRuns:          s.OverlayLayerRuns - o.OverlayLayerRuns,
	}
}

// NewGroupFloors allocates a grouped run's per-bucket floor table (the
// components.aggregations[*].groups tally), counting the allocation.
// Every grouped arm — the serial streaming scan and both parallel
// reducers — allocates through it, and only when the plan computes
// Groups.
func NewGroupFloors() map[string][]SlotFloor {
	workGroupFloorBuilds.Add(1)
	return make(map[string][]SlotFloor)
}
