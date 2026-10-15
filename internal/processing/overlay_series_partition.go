package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/types"
)

// Per-partition SERIES overlay fold (U46 E2-S1; OVERLAY_YOY E2-S3).
//
// With two or more groupers the grouped-Process host is the composite
// rows. A kind that reads the host as an ordered series (the prior,
// rolling, baseline and sibling pairs) or scores each row against the
// host's total (the three _TOTAL kinds) — seriespartition.Kinds — runs
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
//     the first partition's (the eleven kinds set only Baseline, a
//     per-kind constant — a _TOTAL kind's total, mean and SD stay
//     per-partition figures and never reach the layer summary).
//   - Warnings: one per code, the first raising partition's message
//     and Details, with `group_count` restated as the whole host's and
//     `partitions` the number of partitions that raised it — the
//     handlers' "one per layer" discipline holds across partitions.
//
// One partition (one grouper, `partition_by: []`, or every row in one
// bucket) runs the handler on the host itself: byte-identical to the
// unpartitioned walk.

// seriesPartitionPlan is how one spec splits the grouped host: the key
// indices it partitions on by key equality (none: one walk), and, for
// OVERLAY_YOY, the one key element each sub-host keeps (project >= 0).
type seriesPartitionPlan struct {
	on      []int
	project int
}

// noPartition is one walk over the whole host with full keys.
var noPartition = seriesPartitionPlan{project: -1}

// prefixPlan partitions on the first depth key elements.
func prefixPlan(depth int) seriesPartitionPlan {
	plan := seriesPartitionPlan{project: -1}
	for j := 0; j < depth; j++ {
		plan.on = append(plan.on, j)
	}
	return plan
}

// callSeriesHandler runs handler over host; a nil handler is the
// registry-aware SERIES OVERLAY_FORMULA (applyFormulaSeriesWith), handed
// a COPY of exts. The formula compile captures its registry (the lookup
// builtin is a method value), so passing exts itself leaks it, and
// escape analysis then heap-allocates the grouped tail and the
// streaming grouped path's per-run state on every run, overlays or not
// (TestStreamingGrouped_SingleGrouperAllocCeiling).
func callSeriesHandler(handler seriesOverlayHandler, exts *ExtensionRegistry, spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	if handler != nil {
		return handler(spec, host)
	}
	var reg *ExtensionRegistry
	if exts != nil {
		cp := *exts
		reg = &cp
	}
	return applyFormulaSeriesWith(spec, host, reg)
}

// runSeriesHandlerPartitioned runs handler over host once per partition
// of plan.on and merges the per-partition layers. A projecting plan
// (OVERLAY_YOY, U46 E2-S3) hands each sub-host only the date key
// element — its key, group field and grouper kind — so the unchanged
// single-date handler runs inside every segment; each entry then gets
// its full composite key back.
func runSeriesHandlerPartitioned(handler seriesOverlayHandler, exts *ExtensionRegistry, spec *types.OverlaySpec, host *SeriesHostView, plan seriesPartitionPlan) (types.OverlayLayer, []types.OverlayWarning, error) {
	if host == nil {
		return callSeriesHandler(handler, exts, spec, host)
	}
	project := plan.project
	if project >= 0 && len(host.grouperKinds) <= 1 {
		project = -1 // one key element already: nothing to drop
	}
	parts := seriespartition.PartitionOn(host.groupKeys, plan.on)
	if len(parts) <= 1 && project < 0 {
		return callSeriesHandler(handler, exts, spec, host)
	}
	entries := make([]types.SeriesEntry, host.GroupCount())
	var (
		merged   types.OverlayLayer
		summary  *types.OverlaySummary
		warnings []types.OverlayWarning
		raised   = map[string]int{}
	)
	for p, rows := range parts {
		layer, ws, err := callSeriesHandler(handler, exts, spec, host.subset(rows, project))
		if err != nil {
			return types.OverlayLayer{}, nil, err
		}
		if layer.Payload.Series == nil || len(layer.Payload.Series.Entries) != len(rows) {
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"overlay "+string(spec.Kind)+" emitted a series payload misaligned with its partition",
				map[string]any{"kind": string(spec.Kind), "partition": p})
		}
		for j, e := range layer.Payload.Series.Entries {
			if project >= 0 {
				if full := host.groupKeys[rows[j]]; full != nil {
					e.Key = append(types.AxisKey(nil), full...)
				}
			}
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
// group fields and grouper kinds unchanged — or, with project >= 0,
// each key, the group fields and the grouper kinds narrowed to that one
// element.
func (h *SeriesHostView) subset(rows []int, project int) *SeriesHostView {
	keys := make([]types.AxisKey, len(rows))
	for j, i := range rows {
		keys[j] = h.groupKeys[i]
		if project >= 0 {
			var v any
			if project < len(keys[j]) {
				v = keys[j][project]
			}
			keys[j] = types.AxisKey{v}
		}
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
	fields, kinds := h.groupFields, h.grouperKinds
	if project >= 0 {
		fields, kinds = nil, nil
		if project < len(h.groupFields) {
			fields = []string{h.groupFields[project]}
		}
		if project < len(h.grouperKinds) {
			kinds = []types.GroupType{h.grouperKinds[project]}
		}
	}
	return &SeriesHostView{groupKeys: keys, resolver: resolver, groupFields: fields, grouperKinds: kinds}
}

// seriesPartitionFor is how spec splits the grouped host: OVERLAY_YOY
// partitions on every non-date key and projects onto the date key
// (seriespartition.YoY, which also refuses zero / 2+ date groupers and
// a partition_by other than the non-date keys); a kind that runs per
// partition (seriespartition.Applies on its route) on the leading
// seriespartition.Depth keys; any other kind walks the whole host.
func seriesPartitionFor(req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) (seriesPartitionPlan, error) {
	if route == types.OverlayKindYoY {
		date, on, err := seriespartition.YoY(req.Groups, isDateGrouper, spec, index)
		if err != nil {
			return seriesPartitionPlan{}, err
		}
		return seriesPartitionPlan{on: on, project: date}, nil
	}
	if !seriespartition.Applies(route) {
		return noPartition, nil
	}
	depth, err := seriespartition.Depth(req.Groups, spec, index)
	if err != nil {
		return seriesPartitionPlan{}, err
	}
	return prefixPlan(depth), nil
}

// isDateGrouper is OVERLAY_YOY's runtime date-grouper test: the
// authored type (a hidden GROUP_DATE never reaches the fold — grouping
// refuses it first).
func isDateGrouper(g *types.Group) bool { return g.Type == types.GROUP_DATE }
