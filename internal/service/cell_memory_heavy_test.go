//go:build heavy

package service

import (
	"fmt"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstabMemory_CellChargeTable (U51 E5-S9) pins the crosstab's
// per-cell charges — limits.CrosstabSlotBytes over the per-aggregator
// table's state and cell entries, limits.CrosstabAxisKeyBytes,
// limits.CrosstabAuxSlotBytes — against the heap. Every built-in
// aggregator usable as a crosstab cell measure (a vector aggregator
// selecting one element) runs on the fused arm when the fusion gate
// admits it and always on the buffered arm, weighted and not,
// components on and off, over a 60 x 60 grid with every cell
// populated; the run's peak live heap must not exceed what
// descx.EstimateMemory charges for it (cellMemoryCharge). Shape cases
// then cover what the grid alone does not: one-key-wide grids (the axis
// keys), margins, several measures, margin_aggregations, the
// normalize denominators, a value-state measure at several records a
// cell, and a vector element cell over two shards — the blocked merge's
// fragments and the rows a shard starting mid-block buffers — and
// sparse grids over unbounded axes, records on the diagonal of an
// n x n grid (U51 E5-S11): the dense grid's empty positions, charged
// by limits.CrosstabGridPositionBytes.
func TestCrosstabMemory_CellChargeTable(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	ext := New(nil).extensions
	const side = 60
	for _, at := range types.AllAggregationTypes() {
		cell := cellSlot(at, "a", 3)
		probe := cellMemoryCase{measures: []*types.Aggregation{cell}, rows: side, cols: side}
		fusable, _ := processing.CanFuseCrosstab(probe.request(), probe.schema(), ext)
		perCell := []int{1}
		if descx.ValueStateAggregation(at) || vectorAggregation(at) {
			perCell = []int{1, 4}
		}
		for _, buffered := range []bool{false, true} {
			if !buffered && !fusable {
				continue
			}
			for _, w := range []bool{false, true} {
				for _, off := range []bool{false, true} {
					for _, k := range perCell {
						crosstabMemoryCheck(t, string(at), cellMemoryCase{measures: []*types.Aggregation{cell}, weighted: w, off: off,
							buffered: buffered, rows: side, cols: side, perCell: k})
					}
				}
			}
		}
	}
	sum, count, avg := cellSlot(types.AGG_SUM, "s", 0), cellSlot(types.AGG_COUNT, "c", 0), cellSlot(types.AGG_AVERAGE, "m", 0)
	vec := cellSlot(types.AGG_VEC_MEAN, "v", aggMemoryMaxMembers)
	distinct := cellSlot(types.AGG_DISTINCT_COUNT, "d", 0)
	for _, buffered := range []bool{false, true} {
		for _, w := range []bool{false, true} {
			for _, off := range []bool{false, true} {
				base := cellMemoryCase{weighted: w, off: off, buffered: buffered, perCell: 1}
				shape := func(name string, c cellMemoryCase) {
					c.weighted, c.off, c.buffered = w, off, buffered
					crosstabMemoryCheck(t, name, c)
				}
				for _, dims := range [][2]int{{4_000, 1}, {1, 4_000}} {
					for _, m := range []*types.Aggregation{count, vec} {
						for _, margins := range []bool{false, true} {
							c := base
							c.measures, c.margins, c.rows, c.cols = []*types.Aggregation{m}, margins, dims[0], dims[1]
							shape(fmt.Sprintf("%s margins=%v", m.Type, margins), c)
						}
					}
				}
				c := base
				c.measures, c.margins, c.rows, c.cols = []*types.Aggregation{sum, count, avg}, true, 400, 10
				shape("3 measures, margins", c)
				c.measures = []*types.Aggregation{sum, vec}
				c.aux = []*types.Aggregation{cellSlot(types.AGG_COUNT, "ac", 0), cellSlot(types.AGG_AVERAGE, "am", 0),
					cellSlot(types.AGG_DISTINCT_COUNT, "ad", 0), cellSlot(types.AGG_VEC_MEAN, "av", aggMemoryMaxMembers)}
				shape("2 measures, 4 margin_aggregations", c)
				c = base
				c.measures, c.margins, c.nested, c.rows, c.cols = []*types.Aggregation{sum}, true, true, 100, 40
				shape("normalize_level + normalize_within", c)
				c = base
				c.measures, c.rows, c.cols, c.perCell, c.margins = []*types.Aggregation{distinct}, 30, 30, 16, true
				shape("value state, 16 records a cell, margins", c)
				c = base
				c.measures, c.rows, c.cols, c.perCell, c.shards, c.margins = []*types.Aggregation{vec}, 60, 60, 3, 2, true
				shape("vector element over two shards", c)
				// Sparse grids over unbounded axes (U51 E5-S11): n
				// records on the diagonal of an n x n grid, every other
				// position empty — the dense grid's charge
				// (CrosstabGridPositionBytes), not the cells', carries
				// them. 300 rows sit just past a slice growth point.
				for _, side := range []int{300, 1_000} {
					c = base
					c.measures, c.diagonal, c.rows, c.cols = []*types.Aggregation{count}, true, side, side
					shape(fmt.Sprintf("sparse diagonal %d", side), c)
				}
				c = base
				c.measures, c.diagonal, c.rows, c.cols, c.margins = []*types.Aggregation{sum, vec}, true, 500, 500, true
				shape("sparse diagonal, 2 measures, margins", c)
			}
		}
	}
}
