package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/types"
)

// Per-partition SERIES overlay fold (U46 E2-S1).
//
// With two or more groupers the grouped-Process host is the composite
// rows. A kind that reads the host as an ordered series (the prior,
// rolling, baseline and sibling pairs — seriespartition.Kinds) runs
// once per PARTITION: the rows sharing the first `depth` key elements
// (seriespartition.Depth: every key but the last by default, an
// explicit leading prefix, or [] for none). Each partition is a
// sub-host — its rows in host order, the full composite key tuple and
// the host's group fields / grouper kinds — over which the UNCHANGED
// handler runs; the entries land back on the host ordinals they came
// from, so the layer stays aligned to host rows element for element.
//
// Partitions are built by key equality (seriespartition.Partition),
// never contiguity: a user `sort` can interleave buckets.
//
// Merge rules (one layer per spec, as before):
//
//   - Entries: partition p's entry j is host ordinal parts[p][j].
//   - Summary: Min / Max over the partitions' Min / Max, Count summed
//     (each handler counts its present, non-NaN entries, so the merged
//     figures equal one pass over every entry); every other field is
//     the first partition's (the eight kinds set only Baseline, a
//     per-kind constant).
//   - Warnings: one per code, the first raising partition's message
//     and Details, with `group_count` restated as the whole host's and
//     `partitions` the number of partitions that raised it — the
//     handlers' "one per layer" discipline holds across partitions.
//
// One partition (one grouper, `partition_by: []`, or every row in one
// bucket) runs the handler on the host itself: byte-identical to the
// unpartitioned walk.

// runSeriesHandlerPartitioned runs handler over host once per partition
// of its first depth key elements and merges the per-partition layers.
func runSeriesHandlerPartitioned(handler seriesOverlayHandler, spec *types.OverlaySpec, host *SeriesHostView, depth int) (types.OverlayLayer, []types.OverlayWarning, error) {
	if depth <= 0 || host == nil {
		return handler(spec, host)
	}
	parts := seriespartition.Partition(host.groupKeys, depth)
	if len(parts) <= 1 {
		return handler(spec, host)
	}
	entries := make([]types.SeriesEntry, host.GroupCount())
	var (
		merged   types.OverlayLayer
		summary  *types.OverlaySummary
		warnings []types.OverlayWarning
		raised   = map[string]int{}
	)
	for p, rows := range parts {
		layer, ws, err := handler(spec, host.subset(rows))
		if err != nil {
			return types.OverlayLayer{}, nil, err
		}
		if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != len(rows) {
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"overlay "+string(spec.Kind)+" emitted a series payload misaligned with its partition",
				map[string]any{"kind": string(spec.Kind), "partition": p})
		}
		for j, e := range layer.Payload.Series.Entries {
			entries[rows[j]] = e
		}
		if p == 0 {
			merged = layer
			if layer.Summary != nil {
				s := *layer.Summary
				summary = &s
			}
		} else {
			summary = mergeSeriesSummary(summary, layer.Summary)
		}
		for _, w := range ws {
			if at, ok := raised[w.Code]; ok {
				warnings[at].Details["partitions"] = warnings[at].Details["partitions"].(int) + 1
				continue
			}
			details := make(map[string]any, len(w.Details)+1)
			for k, v := range w.Details {
				details[k] = v
			}
			if _, ok := details["group_count"]; ok {
				details["group_count"] = host.GroupCount()
			}
			details["partitions"] = 1
			w.Details = details
			raised[w.Code] = len(warnings)
			warnings = append(warnings, w)
		}
	}
	merged.Payload.Series = &types.SeriesPayload{Entries: entries}
	merged.Summary = summary
	return merged, warnings, nil
}

// mergeSeriesSummary folds next into acc: Min / Max widen, Count sums;
// acc's other fields stand.
func mergeSeriesSummary(acc, next *types.OverlaySummary) *types.OverlaySummary {
	if next == nil {
		return acc
	}
	if acc == nil {
		s := *next
		return &s
	}
	if next.Min != nil && (acc.Min == nil || *next.Min < *acc.Min) {
		v := *next.Min
		acc.Min = &v
	}
	if next.Max != nil && (acc.Max == nil || *next.Max > *acc.Max) {
		v := *next.Max
		acc.Max = &v
	}
	if next.Count != nil {
		c := *next.Count
		if acc.Count != nil {
			c += *acc.Count
		}
		acc.Count = &c
	}
	return acc
}

// subset is the sub-host over host ordinals rows (ascending): their key
// tuples in order, the resolver remapped onto them, and the host's
// group fields and grouper kinds unchanged.
func (h *SeriesHostView) subset(rows []int) *SeriesHostView {
	keys := make([]types.AxisKey, len(rows))
	for j, i := range rows {
		keys[j] = h.groupKeys[i]
	}
	var resolver func(int) (float64, bool)
	if h.resolver != nil {
		resolver = func(j int) (float64, bool) {
			if j < 0 || j >= len(rows) {
				return 0, false
			}
			return h.resolver(rows[j])
		}
	}
	return &SeriesHostView{groupKeys: keys, resolver: resolver, groupFields: h.groupFields, grouperKinds: h.grouperKinds}
}

// seriesPartitionDepth is the leading-key depth spec partitions the
// grouped host by: seriespartition.Depth for a kind that runs per
// partition (seriespartition.Applies on its route), else 0.
func seriesPartitionDepth(req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) (int, error) {
	if !seriespartition.Applies(route) {
		return 0, nil
	}
	return seriespartition.Depth(req.Groups, spec, index)
}
