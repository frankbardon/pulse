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

// slotTotalComponents builds the cohort-wide slot-level entries of a
// grouped run: one per aggregation slot in declared order, carrying
// Label, the {n, n_null} floor tallied through FieldPresent (never
// NumericValue — a set column has no numeric value) and, on a weighted
// slot, the weighted floor keys — all over records, the filter-passing
// set. Operator stays nil: a grouped slot's operator figures are per
// bucket.
func slotTotalComponents(aggs []*types.Aggregation, records []*Record) []types.AggregationComponents {
	if len(aggs) == 0 {
		return nil
	}
	type floor struct{ n, nNull int }
	floors := make(map[string]floor, len(aggs))
	out := make([]types.AggregationComponents, 0, len(aggs))
	for _, agg := range aggs {
		fl, ok := floors[agg.Field]
		if !ok {
			for _, r := range records {
				if FieldPresent(r, agg.Field) {
					fl.n++
				} else {
					fl.nNull++
				}
			}
			floors[agg.Field] = fl
		}
		entry := types.AggregationComponents{Label: agg.Label, N: fl.n, NNull: fl.nNull}
		wf := NewWeightFloor(agg)
		if wf.spec != nil {
			for _, r := range records {
				wf.Observe(r, agg.Field)
			}
			wf.Stamp(&entry)
		}
		out = append(out, entry)
	}
	return out
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
