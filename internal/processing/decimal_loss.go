package processing

import (
	"fmt"
	"sort"
	"sync"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// decimalLossTally counts, per aggregation slot, the cells whose
// decimal aggregate fell back to f64 (decimalAggResult.FellBack): an
// AGG_AVERAGE whose running sum, or an AGG_VARIANCE / AGG_STDDEV whose
// two-pass intermediate, would overflow decimal128(38) (#804). Every
// decimal aggregate — ungrouped, grouped, crosstab cells, measures and
// margin aggregations — runs through AggregateDecimalSlot at one of two
// sites (aggregateWithComponents, runCellAggregation), and both note
// here; the decimal fold is buffered-only, so the streaming arms never
// reach it and a shard archive's or a parallel decode's records reach it
// as one buffered set.
type decimalLossTally struct {
	mu    sync.Mutex
	cells map[decimalLossKey]int
}

type decimalLossKey struct {
	label, aggregation, field string
}

// note records one fallen-back cell of agg (no-op unless r fell back).
func (t *decimalLossTally) note(agg *types.Aggregation, r decimalAggResult) {
	if t == nil || agg == nil || !r.FellBack {
		return
	}
	label := agg.Label
	if label == "" {
		label = types.AggregationLabelOf(agg)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cells == nil {
		t.cells = map[decimalLossKey]int{}
	}
	t.cells[decimalLossKey{label: label, aggregation: string(agg.Type), field: agg.Field}]++
}

// warnings is one PULSE_DECIMAL_PRECISION_LOSS per slot that fell back,
// ordered by label, aggregation, field — so the list is the same
// whatever order the cells were folded in. details {label, aggregation,
// field, cells}: cells counts the fallen-back figures (rows, crosstab
// cells and margins) of that slot.
func (t *decimalLossTally) warnings() []*types.ResponseWarning {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.cells) == 0 {
		return nil
	}
	keys := make([]decimalLossKey, 0, len(t.cells))
	for k := range t.cells {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.label != b.label {
			return a.label < b.label
		}
		if a.aggregation != b.aggregation {
			return a.aggregation < b.aggregation
		}
		return a.field < b.field
	})
	out := make([]*types.ResponseWarning, 0, len(keys))
	for _, k := range keys {
		n := t.cells[k]
		out = append(out, &types.ResponseWarning{
			Code: string(errors.PULSE_DECIMAL_PRECISION_LOSS),
			Message: fmt.Sprintf("%s %q over decimal128 field %q fell back to f64 in %d figure(s): an intermediate would overflow decimal128(38), so those figures are floats, not exact decimals",
				k.aggregation, k.label, k.field, n),
			Details: map[string]any{"label": k.label, "aggregation": k.aggregation, "field": k.field, "cells": n},
		})
	}
	return out
}
