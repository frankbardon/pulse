package descriptor

import (
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// return_precision.go derives the REQUEST-DEPENDENT half of the
// Return.precision exemption: the response nodes whose floats carry
// integer semantics because of what this request asked for — a count
// aggregation's column. The response-type half (nodes that are counts on
// every request, e.g. matrices[*].auxiliary.n) is declared beside the
// encoder in types/return_shape.go (precisionExempt).
//
// Exempting is the safe side: an exempt float is written at full
// precision, never wrong, only less compact — so a weighted count (a
// fractional Σw) riding an exempt column costs bytes, not accuracy.

// countSemanticAggregations is the DECLARED registry of built-in
// aggregators whose finished value is a count carried as a float64.
// TestCountSemanticAggregations_Classified requires every registered
// aggregation type to be classified, so a new count aggregator cannot
// silently get its column rounded. An extension aggregator is never
// listed: its column rounds like any measure.
var countSemanticAggregations = map[types.AggregationType]bool{
	types.AGG_COUNT:               true,
	types.AGG_DISTINCT_COUNT:      true,
	types.AGG_NULL_COUNT:          true,
	types.AGG_MODE_COUNT:          true,
	types.AGG_FREQUENCY:           true,
	types.AGG_SET_FREQUENCY:       true,
	types.AGG_SET_CARDINALITY_SUM: true,
	types.AGG_SET_DISTINCT_VALUES: true,
}

func returnKeyPath(names ...string) returnplan.Path {
	segs := make([]returnplan.Segment, 0, len(names))
	for _, n := range names {
		if n == "[*]" {
			segs = append(segs, returnplan.Elem())
			continue
		}
		segs = append(segs, returnplan.Key(n))
	}
	return returnplan.Path{Segments: segs}
}

// returnExactPaths lists the nodes of req's response whose floats keep
// full precision under Return.precision: each count aggregation's
// `data[*].<label>` column; a count crosstab cell (normalize none) in
// both arms — the matrix cells, margins and grand total, and the
// long-form `data[*].<label>` column; and each count auxiliary margin
// aggregation's figures under `components.crosstab`. It reads the
// DEFAULTS-RESOLVED request (the facade's request after the engine ran),
// so a defaulted count aggregator is recognised.
func returnExactPaths(req *types.Request) []returnplan.Path {
	if req == nil {
		return nil
	}
	var out []returnplan.Path
	for _, agg := range req.Aggregations {
		if agg != nil && countSemanticAggregations[agg.Type] {
			out = append(out, returnKeyPath("data", "[*]", types.AggregationLabelOf(agg)))
		}
	}
	if ct := req.Crosstab; ct != nil {
		if ct.Cell != nil && countSemanticAggregations[ct.Cell.Type] &&
			(ct.Normalize == "" || ct.Normalize == types.CrosstabNormalizeNone) {
			out = append(out,
				returnKeyPath("data", "[*]", types.AggregationLabelOf(ct.Cell)),
				returnKeyPath("crosstab", "matrix", "cells", "[*]", "[*]", "value"),
				returnKeyPath("crosstab", "matrix", "row_margins", "[*]", "value"),
				returnKeyPath("crosstab", "matrix", "column_margins", "[*]", "value"),
				returnKeyPath("crosstab", "matrix", "grand_total", "value"),
			)
		}
		for _, m := range ct.MarginAggregations {
			if m == nil || !countSemanticAggregations[m.Type] {
				continue
			}
			label := types.AggregationLabelOf(m)
			out = append(out,
				returnKeyPath("components", "crosstab", "row_margin_aggregations", "[*]", label, "value"),
				returnKeyPath("components", "crosstab", "column_margin_aggregations", "[*]", label, "value"),
				returnKeyPath("components", "crosstab", "grand_total_aggregations", label, "value"),
			)
		}
	}
	return out
}
