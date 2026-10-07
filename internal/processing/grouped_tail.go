package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// GroupKeyer is the ONE key-derivation dispatch for a single-grouper
// streaming run: a MultiKeyStreamingGrouper (GROUP_SET_PER_ELEMENT) fans
// the record into one key per selected label, a StreamingGrouper yields
// one key. Resolved once per run (NewGroupKeyer) so the per-record hot
// loop does no interface assertion.
//
// Shared by the serial streaming-grouped path and both parallel reducers
// so a multi-key grouper cannot be refused, or collapsed to a single key,
// on one arm only.
type GroupKeyer struct {
	single StreamingGrouper
	multi  MultiKeyStreamingGrouper
	buf    [1]string
}

// NewGroupKeyer resolves grouper's key interface; a grouper implementing
// neither is PROCESSING_INTERNAL.
func NewGroupKeyer(grouper Grouper) (*GroupKeyer, error) {
	if multi, ok := grouper.(MultiKeyStreamingGrouper); ok {
		return &GroupKeyer{multi: multi}, nil
	}
	if single, ok := grouper.(StreamingGrouper); ok {
		return &GroupKeyer{single: single}, nil
	}
	return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
		fmt.Sprintf("grouper %T implements neither StreamingGrouper nor MultiKeyStreamingGrouper", grouper))
}

// Keys returns the bucket keys for r. ok=false means the record lands in
// no bucket (null key, include rejection, empty fan-out). A single-key
// result aliases the keyer's scratch and is valid until the next call.
func (k *GroupKeyer) Keys(r *Record, field string) ([]string, bool, error) {
	if k.multi != nil {
		keys, ok, err := k.multi.KeysForRow(r, field)
		if err != nil || !ok || len(keys) == 0 {
			return nil, false, err
		}
		return keys, true, nil
	}
	key, ok, err := k.single.KeyForRow(r, field)
	if err != nil || !ok {
		return nil, false, err
	}
	k.buf[0] = key
	return k.buf[:], true, nil
}

// GroupedTail is everything a single-grouper streaming run has once the
// records are consumed: the grouper instance KeyForRow/KeysForRow drove
// (its live state backs Components.Groupers), the per-key aggregator
// buckets, and the run counters.
type GroupedTail struct {
	Group   *types.Group
	Grouper Grouper
	Buckets map[string][]OnlineAggregator

	// SlotTotals is each aggregation slot's COHORT-WIDE floor tally over
	// every filter-passing record (bucketed or not), and BucketFloors the
	// per-bucket per-slot tallies minted with each bucket — together the
	// floors behind Components.Aggregations' slot entries and groups[].
	// SlotTotals is nil unless the plan computes Aggs, BucketFloors
	// unless it computes Groups (NewGroupFloors); every caller tallies
	// them through ObserveSlotFloors (the parallel reducers merge them
	// with MergeSlotFloors), so the figures match whatever the worker
	// count.
	SlotTotals   []SlotFloor
	BucketFloors map[string][]SlotFloor

	TotalRows, FilteredRows, NullRecords int64
	FilterCounters                       []FilterPassCounters

	// Assignments counts (record, bucket) routings — one per key a
	// record was keyed into, so a fan-out record counts once per
	// bucket. It is Components.Groupers' TotalN for a grouper whose
	// MetaGrouper payload carries no "buckets" list (an extension
	// grouper); built-ins derive TotalN from their buckets payload.
	// Every caller sets it — the serial path counts it per record, the
	// parallel reducers sum the per-partition counts — so a bucket-less
	// grouper's floor is identical whatever the worker count.
	Assignments int64

	// ShardCount is the archive's shard count (0 for a single file); it
	// lands on Components.Run.ShardCount.
	ShardCount int
	// Compute is the run's ComputePlan: which Components sub-parts the
	// tail builds. The zero value builds none — callers pass the
	// processor's (or the service's) resolved plan.
	Compute ComputePlan

	// PostTests runs Request.PostTests over the finished rows; nil
	// means none (the parallel reducers' merge gate refuses post-tests).
	PostTests func(rows []map[string]any) ([]*types.TestResult, error)

	// Matrices is the run's per-bucket Request.Matrices state (nil
	// without matrices). Its results are rendered in the FINAL Data row
	// order — an explicit Request.Sort included — spec-major then bucket.
	Matrices *GroupedMatrices

	// Extensions is the instance registry the SERIES overlay fold
	// routes kinds through, so a kind the feature set hides misses
	// like a never-registered one. Nil is the built-in catalog.
	Extensions *ExtensionRegistry
}

// FinalizeGroupedStream is the ONE emission tail of a single-grouper
// streaming run: row order (include order, else sorted keys), the
// Rich-or-scalar lift per cell, an explicit Request.Sort, post-tests,
// Components (per-group aggregations, groupers, filterers, run) and the
// SERIES overlay fold.
//
// The serial processStreamingGrouped exit and both parallel reducers
// (service.finalizeMergedPartial) call it, so a grouped request answers
// identically whatever Options.ShardWorkers / DecodeWorkers says. Before
// it existed the parallel arm had its own copy of the row loop and
// silently dropped Request.Sort, Request.Overlays, include ordering and
// Components.Groupers.
func FinalizeGroupedStream(req *types.Request, t GroupedTail) (*types.Response, error) {
	labels := make([]string, len(req.Aggregations))
	for i, agg := range req.Aggregations {
		labels[i] = agg.Label
		if labels[i] == "" {
			labels[i] = fmt.Sprintf("%s_%s", agg.Type, agg.Field)
		}
	}

	keys := make([]string, 0, len(t.Buckets))
	for k := range t.Buckets {
		keys = append(keys, k)
	}
	keys = orderKeysByInclude(includeFilterOf(t.Grouper), keys)

	data := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		bucket := t.Buckets[key]
		// +1 reserved for the group key written below.
		row := make(map[string]any, len(bucket)+1)
		for i, oa := range bucket {
			val, err := oa.Finalize()
			if err != nil {
				return nil, err
			}
			row[labels[i]], err = dispatchAggregatorResult(oa, val)
			if err != nil {
				return nil, err
			}
		}
		row[t.Group.Field] = key
		data = append(data, row)
	}

	keys, err := sortGroupedRows(data, req.Sort, keys)
	if err != nil {
		return nil, err
	}
	matrices, matrixComps, err := t.Matrices.finalize(keys)
	if err != nil {
		return nil, err
	}

	var postResults []*types.TestResult
	if t.PostTests != nil {
		var err error
		if postResults, err = t.PostTests(data); err != nil {
			return nil, err
		}
	}

	resp := &types.Response{
		Data: data,
		Metadata: &types.ResponseMetadata{
			TotalRows:    t.TotalRows,
			FilteredRows: t.FilteredRows,
		},
		PostTests: postResults,
		Matrices:  matrices,
	}

	if t.Compute.Aggs && len(req.Aggregations) > 0 {
		// Per-group aggregation Components at TERMINAL flush: the
		// cohort-wide slot floors plus (when the plan computes Groups) one
		// groups[] entry per Data row, in the final (sorted) order — the
		// buffered arm's shape, off the live per-bucket aggregators
		// (Finalized above) and the floor tallies the caller kept beside
		// them.
		var grouped []types.AggregationComponents
		if t.Compute.Groups {
			grouped, err = streamedGroupedAggregationComponents(req.Aggregations, t.SlotTotals, t.Buckets, t.BucketFloors, keys)
			if err != nil {
				return nil, err
			}
		} else {
			grouped = slotFloorTotals(req.Aggregations, t.SlotTotals)
		}
		for _, entry := range grouped {
			attachAggregationComponents(resp, entry)
		}
	}
	if t.Compute.Groupers {
		// One GrouperComponents entry off the grouper's live state;
		// TotalN sums the bucket counts and NNull is every post-filter
		// record that landed in no bucket (null key, include rejection,
		// empty set mask).
		entry, err := buildStreamingGrouperComponents(t.Grouper, t.Group, int(t.FilteredRows), t.Assignments)
		if err != nil {
			return nil, err
		}
		attachGrouperComponents(resp, entry)
	}
	if t.Compute.Filterers {
		attachFiltererComponents(resp, buildFiltererComponents(req.Filterers, t.FilterCounters))
	}
	if t.Compute.Run {
		attachRunComponents(resp, RunCountersInput{
			TotalRecords:    t.TotalRows,
			FilteredRecords: t.FilteredRows,
			NullRecords:     t.NullRecords,
			ShardCount:      t.ShardCount,
		})
	}
	// One MatrixComponents entry per Response.Matrices result (nil when
	// the plan skips them).
	attachMatrixComponents(resp, matrixComps)

	// SERIES-host overlay hook — the same post-finalize wiring as the
	// buffered processRecords path, over the same finished rows. On the
	// serial arm a non-streamable kind is routed buffered by canStream;
	// the parallel arms (CanMergeRequest does not look at overlays) fold
	// it here, which is equivalent because the hook reads only the
	// materialised response (pinned by the shard parity suite).
	if err := applyOverlaysSeriesToResponse(req, resp, t.Extensions); err != nil {
		return nil, err
	}
	return resp, nil
}
