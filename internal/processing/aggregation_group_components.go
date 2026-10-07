package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Per-group aggregator Components for grouped (Request.Groups,
// non-crosstab) runs.
//
// A grouped run reports one AggregationComponents entry per
// Request.Aggregations slot, exactly as an ungrouped run does, with
// two differences: the slot-level floor (n, n_null and the weighted
// floor keys) is the COHORT-WIDE total over every filter-passing
// record, with no Operator map; and Groups carries one
// AggregationGroupComponents per Data row, in the final Data order
// (Request.Sort included), each the figures an ungrouped run over that
// bucket's records reports. The helpers below are shared by every
// execution path that mints buckets, so the per-group shape cannot
// drift between them.

// SlotFloor is one aggregation slot's floor tally: {n, n_null} through
// FieldPresent (never NumericValue — a set column has presence but no
// numeric value) plus the slot's weighted floor (inert on an unweighted
// slot). It is a plain per-record sum, so partitions merge slot-wise in
// any order. Every grouped arm tallies through it — the buffered
// cohort-wide totals, the streaming-grouped path (cohort-wide and per
// bucket) and the shard / parallel-decode partials — so a slot's floor
// has one implementation whatever the worker count.
type SlotFloor struct {
	field  string
	n      int
	nNull  int
	weight WeightFloor
}

// NewSlotFloors returns one empty tally per aggregation slot, in
// declared order.
func NewSlotFloors(aggs []*types.Aggregation) []SlotFloor {
	if len(aggs) == 0 {
		return nil
	}
	out := make([]SlotFloor, len(aggs))
	for i, agg := range aggs {
		out[i] = SlotFloor{field: agg.Field, weight: NewWeightFloor(agg)}
	}
	return out
}

// ObserveSlotFloors folds one filter-passing record into every slot's
// tally. Call it AFTER row-local attributes land, so a slot over an
// attribute label sees the presence the buffered arm sees. A nil slice
// (components disabled) is inert.
func ObserveSlotFloors(floors []SlotFloor, r *Record) {
	for i := range floors {
		f := &floors[i]
		if FieldPresent(r, f.field) {
			f.n++
		} else {
			f.nNull++
		}
		f.weight.Observe(r, f.field)
	}
}

// MergeSlotFloors folds src's tallies into dst slot-wise.
func MergeSlotFloors(dst, src []SlotFloor) {
	for i := range dst {
		if i >= len(src) {
			return
		}
		dst[i].n += src[i].n
		dst[i].nNull += src[i].nNull
		dst[i].weight.Merge(src[i].weight)
	}
}

// N is the slot's present-input count.
func (f SlotFloor) N() int { return f.n }

// NNull is the slot's null-input count.
func (f SlotFloor) NNull() int { return f.nNull }

// Stamp writes the weighted floor keys onto e; no-op on an unweighted
// slot.
func (f SlotFloor) Stamp(e *types.AggregationComponents) { f.weight.Stamp(e) }

// slotFloorTotals renders cohort-wide slot-level entries from finished
// tallies: Label, {n, n_null} and the weighted floor keys; Operator
// stays nil (a grouped slot's operator figures are per bucket).
func slotFloorTotals(aggs []*types.Aggregation, floors []SlotFloor) []types.AggregationComponents {
	if len(aggs) == 0 {
		return nil
	}
	out := make([]types.AggregationComponents, 0, len(aggs))
	for i, agg := range aggs {
		var f SlotFloor
		if i < len(floors) {
			f = floors[i]
		}
		entry := types.AggregationComponents{Label: agg.Label, N: f.n, NNull: f.nNull}
		f.Stamp(&entry)
		out = append(out, entry)
	}
	return out
}

// slotTotalComponents builds the cohort-wide slot-level entries of a
// buffered grouped run over records, the filter-passing set: one per
// aggregation slot in declared order (see slotFloorTotals).
func slotTotalComponents(aggs []*types.Aggregation, records []*Record) []types.AggregationComponents {
	floors := NewSlotFloors(aggs)
	for _, r := range records {
		ObserveSlotFloors(floors, r)
	}
	return slotFloorTotals(aggs, floors)
}

// streamedGroupedAggregationComponents assembles a streaming or merged
// grouped run's Components.Aggregations from its live per-bucket
// aggregators (already Finalized) and floor tallies, over the FINAL Data
// order. Each bucket entry comes off the same builder the ungrouped
// streaming exit uses (buildAggregationComponents + the weighted floor),
// so it equals the buffered arm's entry for that bucket.
func streamedGroupedAggregationComponents(aggs []*types.Aggregation, totals []SlotFloor, buckets map[string][]OnlineAggregator, floors map[string][]SlotFloor, keys []string) ([]types.AggregationComponents, error) {
	if len(totals) != len(aggs) {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"grouped components: cohort-wide slot floors missing",
			map[string]any{"slots": len(aggs), "floors": len(totals)})
	}
	entries := make(map[string][]types.AggregationComponents, len(keys))
	for _, key := range keys {
		bucket, fl := buckets[key], floors[key]
		if len(bucket) != len(aggs) || len(fl) != len(aggs) {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"grouped components: a bucket has no per-slot floor tally",
				map[string]any{"group_key": key, "slots": len(aggs), "floors": len(fl)})
		}
		row := make([]types.AggregationComponents, len(aggs))
		for i, oa := range bucket {
			e, err := buildAggregationComponents(oa, aggs[i], fl[i].n, fl[i].nNull)
			if err != nil {
				return nil, err
			}
			fl[i].Stamp(&e)
			row[i] = e
		}
		entries[key] = row
	}
	return GroupedAggregationComponents(slotFloorTotals(aggs, totals), entries, keys)
}

// AggregationGroupEntry converts one bucket's slot entry — built by the
// same builder an ungrouped run uses (BuildAggregationComponents plus
// WeightFloor.Stamp) — into its per-group form keyed by the bucket key.
func AggregationGroupEntry(key string, e types.AggregationComponents) types.AggregationGroupComponents {
	return types.AggregationGroupComponents{
		GroupKey:       types.AxisKey{key},
		N:              e.N,
		NNull:          e.NNull,
		SumWeights:     e.SumWeights,
		NEff:           e.NEff,
		NWeightInvalid: e.NWeightInvalid,
		Operator:       e.Operator,
	}
}

// GroupedAggregationComponents assembles a grouped run's
// Components.Aggregations: totals are the cohort-wide slot entries (one
// per slot, declared order), buckets maps each bucket key to its
// per-slot entries (same order), and keys is the FINAL Data order —
// after sortGroupedRows — so groups[i] describes Data[i]. totals is
// returned with Groups filled in.
func GroupedAggregationComponents(totals []types.AggregationComponents, buckets map[string][]types.AggregationComponents, keys []string) ([]types.AggregationComponents, error) {
	for i := range totals {
		groups := make([]types.AggregationGroupComponents, 0, len(keys))
		for _, key := range keys {
			entries, ok := buckets[key]
			if !ok || i >= len(entries) {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
					"grouped components: a Data row's bucket has no aggregation components",
					map[string]any{"group_key": key, "slot": i})
			}
			groups = append(groups, AggregationGroupEntry(key, entries[i]))
		}
		totals[i].Groups = groups
	}
	return totals, nil
}
