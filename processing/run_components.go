package processing

import "github.com/frankbardon/pulse/types"

// run_components.go centralises the run-pass accumulator that populates
// Response.Components.Run (the typed cohort-level counters: TotalRecords,
// FilteredRecords, NullRecords, ShardCount, PartialCohortReason). E2-S11.
//
// Coexistence with Response.Metadata is deliberate: Metadata keeps the
// non-numerical run facts surfaced by the orchestrator (cohort filename),
// while RunComponents carries the typed counters consumers compute
// against (joins, ratios, partial-cohort diagnostics). The two slots are
// promoted on every successful Process call; Metadata.TotalRows ==
// Components.Run.TotalRecords by construction so consumers can derive
// either side without drift.
//
// "Primary aggregation field" definition (locked in code per the E2-S11
// acceptance criteria comment): NullRecords counts records whose primary
// field was null on the post-filter record set. The primary field is the
// FIRST Aggregation slot's Field; if no aggregators, the FIRST Group
// slot's Field; if neither, the helper leaves NullRecords at 0. This
// matches the convention the per-slot AggregationComponents universal
// floor already locks (the first agg's nNull counter), so callers that
// want a richer per-slot view consult Components.Aggregations directly.
//
// The helper deliberately ALWAYS allocates a RunComponents on successful
// Process completion (even when every typed counter is zero) so
// consumers can rely on Response.Components.Run being present after a
// successful run. Inner-field omitempty tags keep the wire form clean
// when individual counters happen to be zero (ShardCount /
// PartialCohortReason); TotalRecords / FilteredRecords / NullRecords are
// NOT omitempty so the universal-floor counters always appear.

// RunCountersInput is the orchestrator-side input shape carrying every
// counter the run accumulated by terminal flush. The helper materialises
// a types.RunComponents from this shape and attaches it to the response.
// All four processing/processor.go exit points (processStreaming /
// processStreamingGrouped / processStreamingTwoPass / processRecords)
// compute these values inline and pass them in; the service-layer
// shard-parallel / parallel-buffered exits compute them via the merged
// shardPartial state and pass them in via the same shape.
type RunCountersInput struct {
	// TotalRecords is the total number of records decoded from the
	// cohort BEFORE the filter pass (mirrors Response.Metadata.TotalRows
	// on every Pulse execution path).
	TotalRecords int64

	// FilteredRecords is the post-all-filters record count. When no
	// filterers are declared this equals TotalRecords by definition;
	// when filterers are declared this is the last filterer's nOut (the
	// records admitted to the downstream aggregation / grouping stage).
	FilteredRecords int64

	// NullRecords counts records whose primary aggregation field was
	// null on the post-filter record set. See package comment for the
	// "primary field" definition.
	NullRecords int64

	// ShardCount is the number of shards merged when the cohort is a
	// shard archive. Zero for single-file cohorts.
	ShardCount int

	// PartialCohortReason carries a free-form diagnostic explaining why
	// the run consumed less than the full cohort. Empty when the run
	// completed fully.
	PartialCohortReason string
}

// attachRunComponents writes a populated *types.RunComponents onto
// resp.Components.Run, allocating resp.Components lazily when it is nil.
// Always allocates and writes the Run struct on successful Process
// completion so consumers can rely on its presence; inner-field
// omitempty tags keep zero values out of the wire form when applicable
// (ShardCount / PartialCohortReason). The helper is a no-op when resp
// is nil — defensive guard for path-not-yet-wired callers.
func attachRunComponents(resp *types.Response, in RunCountersInput) {
	if resp == nil {
		return
	}
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	resp.Components.Run = &types.RunComponents{
		TotalRecords:        in.TotalRecords,
		FilteredRecords:     in.FilteredRecords,
		NullRecords:         in.NullRecords,
		ShardCount:          in.ShardCount,
		PartialCohortReason: in.PartialCohortReason,
	}
}

// primaryNullFieldName returns the name of the field whose null count
// drives Components.Run.NullRecords. Resolution order matches the
// package-level "primary aggregation field" rule: first aggregator's
// Field, else first grouper's Field, else empty string (caller leaves
// NullRecords at 0).
func primaryNullFieldName(req *types.Request) string {
	if req == nil {
		return ""
	}
	for _, agg := range req.Aggregations {
		if agg != nil && agg.Field != "" {
			return agg.Field
		}
	}
	for _, grp := range req.Groups {
		if grp != nil && grp.Field != "" {
			return grp.Field
		}
	}
	return ""
}

// countNullsBuffered counts records whose named field is null over the
// supplied (typically post-filter) record slice. Used by the buffered
// processRecords exit when no streaming-path nNull counter is available
// — the streaming exits already track per-aggregator nNull inline so
// they pass it through RunCountersInput directly. Returns 0 when the
// field name is empty (no primary field resolvable from req).
func countNullsBuffered(records []*Record, field string) int64 {
	if field == "" {
		return 0
	}
	var n int64
	for _, r := range records {
		if r == nil {
			continue
		}
		if r.IsNull(field) {
			n++
		}
	}
	return n
}
