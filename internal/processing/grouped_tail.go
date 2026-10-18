package processing

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/window"
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

// GroupedTail is everything a streaming grouped run has once the
// records are consumed: the grouper instances KeyForRow/KeysForRow
// drove (their live state backs Components.Groupers), the per-key
// aggregator buckets, and the run counters.
type GroupedTail struct {
	// Groups and Groupers are the run's grouper slots in declared
	// order, one grouper instance per Request.Groups entry (len(Groups)
	// == len(Groupers)); each grouper was driven exactly once per
	// filter-passing record, so its live state is its own
	// Components.Groupers entry.
	Groups   []*types.Group
	Groupers []Grouper
	// KeySet is the observed composite keys of a run with two or more
	// groupers, with their per-grouper tuples: Buckets is then keyed by
	// composite key, and every key column is written from the tuple.
	// Nil for one grouper (Buckets keyed by the plain key).
	KeySet  *CompositeKeySet
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

	// Assignments[i] counts grouper i's (record, key) routings — one
	// per key the grouper gave a record, so a fan-out record counts once
	// per key, and a record dropped at another position (null key)
	// still counts here. It is Components.Groupers' TotalN for a
	// grouper whose MetaGrouper payload carries no "buckets" list (an
	// extension grouper); built-ins derive TotalN from their buckets
	// payload. Every caller sets it — the serial path counts it per
	// record, the parallel reducers sum the per-partition counts — so a
	// bucket-less grouper's floor is identical whatever the worker
	// count. One entry per grouper.
	Assignments []int64

	// ShardCount is the archive's shard count (0 for a single file); it
	// lands on Components.Run.ShardCount.
	ShardCount int
	// Compute is the run's ComputePlan: which Components sub-parts the
	// tail builds. The zero value builds none — callers pass the
	// processor's (or the service's) resolved plan.
	Compute ComputePlan

	// PostTests runs Request.PostTests over the finished rows (per
	// partition on a grouped request) and returns the results plus the
	// sparse-partition warnings; nil means none (the parallel reducers'
	// merge gate refuses post-tests).
	PostTests func(rows []map[string]any) ([]*types.TestResult, []*types.ResponseWarning, error)

	// Matrices is the run's per-bucket Request.Matrices state (nil
	// without matrices). Its results are rendered in the FINAL Data row
	// order — an explicit Request.Sort included — spec-major then bucket.
	Matrices *GroupedMatrices

	// Extensions is the instance registry the SERIES overlay fold
	// routes kinds through, so a kind the feature set hides misses
	// like a never-registered one. Nil is the built-in catalog.
	Extensions *ExtensionRegistry
}

// FinalizeGroupedStream is the ONE emission tail of a streaming
// grouped run: row order (include order, else sorted keys — for two or
// more groupers each grouper's native order, outer → inner), the
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
			labels[i] = types.AggregationLabelOf(agg)
		}
	}

	if len(t.Groups) != len(t.Groupers) || len(t.Assignments) != len(t.Groupers) || len(t.Groupers) == 0 {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"grouped stream tail: groups, groupers and assignments disagree",
			map[string]any{"groups": len(t.Groups), "groupers": len(t.Groupers), "assignments": len(t.Assignments)})
	}
	composite := t.KeySet != nil

	keys := make([]string, 0, len(t.Buckets))
	for k := range t.Buckets {
		keys = append(keys, k)
	}
	if composite {
		keys = OrderCompositeGroupKeys(keys, t.KeySet, t.Groupers)
	} else {
		keys = orderKeysByInclude(includeFilterOf(t.Groupers[0]), keys)
	}

	data := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		bucket := t.Buckets[key]
		// Room reserved for the group key column(s) written below.
		row := make(map[string]any, len(bucket)+len(t.Groups))
		for i, oa := range bucket {
			val, err := oa.Finalize()
			if err != nil {
				return nil, err
			}
			if err = writeAggregationResult(row, labels[i], oa, val); err != nil {
				return nil, err
			}
		}
		if composite {
			WriteGroupKeyColumns(row, t.Groups, t.KeySet.Tuple(key))
		} else {
			row[t.Groups[0].Field] = key
		}
		data = append(data, row)
	}

	// The sort follows each grouper key column's key order, read off
	// the run's own groupers (their rank derives from configuration
	// alone), so every worker count sorts as the buffered arm does.
	var keyOrders window.KeyOrders
	if len(req.Sort) > 0 {
		keyOrders = windowKeyOrders(keyOrdersOf(t.Groups, func(i int, _ *types.Group) Grouper { return t.Groupers[i] }))
	}
	keys, err := sortGroupedRows(data, req.Sort, keys, keyOrders)
	if err != nil {
		return nil, err
	}
	matrices, matrixComps, err := t.Matrices.finalize(keys, t.Groups, t.KeySet)
	if err != nil {
		return nil, err
	}

	var postResults []*types.TestResult
	var postWarnings []*types.ResponseWarning
	if t.PostTests != nil {
		var err error
		if postResults, postWarnings, err = t.PostTests(data); err != nil {
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
		Warnings:  postWarnings,
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
			grouped, err = streamedGroupedAggregationComponents(req.Aggregations, t.SlotTotals, t.Buckets, t.BucketFloors, keys, t.KeySet)
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
		// One GrouperComponents entry per grouper slot, each off that
		// grouper's own live state; TotalN sums its bucket counts and
		// NNull is every post-filter record it keyed into no bucket (null
		// key, include rejection, empty set mask) — whatever the other
		// groupers gave the record.
		for i, g := range t.Groupers {
			entry, err := buildStreamingGrouperComponents(g, t.Groups[i], int(t.FilteredRows), t.Assignments[i])
			if err != nil {
				return nil, err
			}
			attachGrouperComponents(resp, entry)
		}
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
	if err := applyOverlaysSeriesToResponse(req, resp, t.Extensions, t.Compute); err != nil {
		return nil, err
	}
	return resp, nil
}
