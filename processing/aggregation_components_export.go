package processing

import (
	"github.com/frankbardon/pulse/types"
)

// aggregation_components_export.go exposes the per-slot
// AggregationComponents builder to service/'s parallel reducers.
//
// The per-shard (Options.ShardWorkers) and per-segment
// (Options.DecodeWorkers) reducers own their own record walk, so they
// track the universal floor {n, n_null} themselves — but the RENDERING
// of a slot (the floor, the Label echo, and the MetaAggregator
// dispatch that fills Operator) must stay a single implementation.
// Response.Components is a documented contract keyed to the request;
// a concurrency knob that changes its shape is the same defect class
// as an aggregator answering a different VALUE in parallel.
//
// Why the reducers can hand a MERGED aggregator straight to this
// builder: the parallel arms merge OPERATOR STATE (MergeableAggregator.
// MergeOnline), not components. After the fold, merged.aggs[i] holds
// the complete state for the whole cohort, so its Components() is
// exactly what the serial instance would have reported — no
// per-operator components merge is needed or attempted. That is also
// why the mergeability classes (Mergeable / Partial / None) do not
// need a second gate here: processing.CanMergeRequest already refuses
// a request whose operator state cannot fold, and every operator that
// clears it folds its components with it.

// BuildAggregationComponents renders one Response.Components.
// Aggregations entry: the universal floor {n, n_null} plus the Label
// echo, and the operator-specific map when agg implements
// MetaAggregator. Aggregators without the sibling get a floor-only
// entry whose Operator stays nil (omitempty on the wire).
//
// n and n_null are the CALLER's per-record tallies — this function
// never derives them — because the orchestrator that walked the
// records is the only thing that knows which slot saw which row.
// Count with FieldPresent, not NumericValue: a set-typed column has
// no numeric value but very much has presence, and asking the wrong
// question moves every respondent into n_null.
func BuildAggregationComponents(agg any, slot *types.Aggregation, n, nNull int) (types.AggregationComponents, error) {
	return buildAggregationComponents(agg, slot, n, nNull)
}

// AttachAggregationComponents appends an entry onto
// resp.Components.Aggregations, allocating the shell as needed.
// Callers MUST gate the build+attach pair on the effective
// DisableComponents decision so the MetaAggregator.Components work is
// skipped rather than built and discarded.
func AttachAggregationComponents(resp *types.Response, entry types.AggregationComponents) {
	attachAggregationComponents(resp, entry)
}
