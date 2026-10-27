//go:build heavy

package service

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// TestGroupMemory_AggregatorStateTable (U51 E5-S7) pins the built-in
// per-aggregator state table (limits.AggregationStateBytes) against the
// heap: for EVERY registered built-in aggregator — weighted and not,
// components on and off, 1 and 4 rows a key — a grouped partition is
// built and folded exactly as the parallel reducers build theirs, a
// second one merged into it on the mergeable types, and the live heap
// the slot adds to a bucket (the case less the same case with no
// aggregation) must not exceed its table entry, on the partition and
// on the merged state. A vector aggregator is measured at 3, 16 and 64
// members, its merge-block fragments (one a key a partition) and the
// rows the second partition buffers charged by
// limits.VectorAggregationBytes. A built-in with no entry fails here; a
// buffered-only one (no per-bucket accumulator) keeps the extension
// ceilings (limits TestAggregationBytes_TableShape). A mixed request
// carrying every streaming built-in at once bounds the slice and
// size-class growth of a wide bucket.
func TestGroupMemory_AggregatorStateTable(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const keys = aggMemoryKeys
	type variant struct {
		weighted, off bool
		rows          int
	}
	var variants []variant
	for _, w := range []bool{false, true} {
		for _, off := range []bool{false, true} {
			for _, rows := range []int{1, 4} {
				variants = append(variants, variant{w, off, rows})
			}
		}
	}
	// The no-aggregation bucket each variant's share is measured over.
	none := map[variant]aggMemoryFigures{}
	for _, v := range variants {
		none[v] = measureAggMemory(t, aggMemoryCase{off: v.off, rows: v.rows, merge: true}, keys)
	}
	for _, at := range types.AllAggregationTypes() {
		measured, buffered := limits.AggregationMeasured(at)
		if !measured {
			t.Errorf("%s: no entry in the per-aggregator state table (limits.aggregatorBytesTable): measure it here and add one", at)
			continue
		}
		if buffered {
			continue // no per-bucket accumulator; the ceilings stand
		}
		memberCounts := []int{0}
		if vectorAggregation(at) {
			memberCounts = []int{3, 16, 64}
		}
		for _, p := range memberCounts {
			for _, v := range variants {
				c := aggMemoryCase{aggs: []*types.Aggregation{aggMemorySlot(at, "a", p)}, weighted: v.weighted, off: v.off, rows: v.rows, merge: at.Mergeable()}
				f := measureAggMemory(t, c, keys)
				entry := float64(limits.AggregationStateBytes(at, v.weighted, int64(p)))
				partFrag := float64(limits.VectorAggregationBytes(at, int64(p), keys, 0)) / keys
				mergedFrag := partFrag
				if c.merge {
					mergedFrag = float64(limits.VectorAggregationBytes(at, int64(p), 2*keys, aggMemoryBufferedRows(keys*v.rows))) / keys
				}
				part := f.partition - none[v].partition
				merged := f.merged - none[v].merged
				t.Logf("%-24s p=%-2d weighted=%-5v off=%-5v rows=%d: partition %6.1f merged %6.1f B a key, entry %4.0f + fragments %6.1f / %6.1f",
					at, p, v.weighted, v.off, v.rows, part, merged, entry, partFrag, mergedFrag)
				if part > entry+partFrag {
					t.Errorf("%s p=%d %+v: a partition's slot holds %.1f B a key, over its entry %.0f + fragments %.1f", at, p, v, part, entry, partFrag)
				}
				if merged > entry+mergedFrag {
					t.Errorf("%s p=%d %+v: the merged slot holds %.1f B a key, over its entry %.0f + fragments %.1f", at, p, v, merged, entry, mergedFrag)
				}
			}
		}
	}
	// Every streaming built-in in one bucket: the bucket's slot and
	// floor slices grow by allocator size class, never by more than the
	// entries' headroom. Weighted, components on: the widest state.
	var mixed []*types.Aggregation
	var charge float64
	members := int64(3)
	for i, at := range types.AllAggregationTypes() {
		if !at.Streamable() {
			continue
		}
		mixed = append(mixed, aggMemorySlot(at, fmt.Sprintf("a%02d", i), int(members)))
		charge += float64(limits.AggregationStateBytes(at, true, members)) + float64(limits.VectorAggregationBytes(at, members, keys, 0))/keys
	}
	f := measureAggMemory(t, aggMemoryCase{aggs: mixed, weighted: true, rows: 1}, keys)
	share := f.partition - none[variant{true, false, 1}].partition
	t.Logf("every streaming built-in (%d slots) weighted: %.1f B a key, entries %.0f", len(mixed), share, charge)
	if share > charge {
		t.Errorf("a bucket of every streaming built-in holds %.1f B a key, over its entries' %.0f", share, charge)
	}
}

// TestGroupMemory_FinalizedRowBytes (U51 E5-S7) is the calibration gate
// for a merged bucket's whole charge — its state plus the finalize
// output it adds, the components-on figure or, with components off, the
// row alone (limits.BucketBytes). Measured as the live heap a key holds
// once the merged state is finalized (state and response both alive, as
// at the run's peak), it must not exceed aggMemoryCharge, components on
// and off: for every streaming built-in alone (weighted and not), for
// AGG_MAX columns just below and just past each growth point of the Data
// row map (8, 14, 28, 56, 112 entries) under one and three groupers, for
// the retention stage's 15 slots and for every streaming built-in in one
// bucket. The merged state alone must not exceed the state charge.
func TestGroupMemory_FinalizedRowBytes(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const keys = aggMemoryKeys
	check := func(name string, c aggMemoryCase) {
		t.Helper()
		f := measureAggMemory(t, c, keys)
		state, final := aggMemoryCharge(c, keys)
		t.Logf("%-34s off=%-5v: state %7.1f / %7.0f, state + output %8.1f / %8.0f (%.2f)", name, c.off, f.merged, state, f.finalized, final, f.finalized/final)
		if f.merged > state {
			t.Errorf("%s off=%v: merged state %.1f B a key, charged %.0f", name, c.off, f.merged, state)
		}
		if f.finalized > final {
			t.Errorf("%s off=%v: finalized %.1f B a key, charged %.0f", name, c.off, f.finalized, final)
		}
	}
	for _, off := range []bool{false, true} {
		for _, at := range types.AllAggregationTypes() {
			if !at.Streamable() {
				continue
			}
			for _, w := range []bool{false, true} {
				check(fmt.Sprintf("%s weighted=%v", at, w), aggMemoryCase{aggs: []*types.Aggregation{aggMemorySlot(at, "a", 3)}, weighted: w, off: off, rows: 1, merge: at.Mergeable()})
			}
		}
		for _, groupers := range []int{1, 3} {
			for _, n := range []int{0, 7, 13, 14, 27, 28, 55, 56, 111, 112} {
				var aggs []*types.Aggregation
				for i := range n {
					aggs = append(aggs, aggMemorySlot(types.AGG_MAX, fmt.Sprintf("m%03d", i), 0))
				}
				check(fmt.Sprintf("%d x AGG_MAX, %d grouper(s)", n, groupers), aggMemoryCase{aggs: aggs, off: off, rows: 1, merge: true, groupers: groupers})
			}
		}
		check("retention stage", aggMemoryCase{aggs: retentionStageAggregations(), off: off, rows: 2, merge: true})
		var mixed []*types.Aggregation
		for i, at := range types.AllAggregationTypes() {
			if at.Streamable() {
				mixed = append(mixed, aggMemorySlot(at, fmt.Sprintf("a%02d", i), 3))
			}
		}
		check("every streaming built-in, weighted", aggMemoryCase{aggs: mixed, weighted: true, off: off, rows: 1})
	}
}

// TestGroupMemory_FinalizeReleasesBuckets (U51 E5-S12, #499): the
// grouped tail consumes the merged buckets as it writes the Data rows,
// so once a grouped response is built the merged partial — still
// reachable from the reducer's frame — holds next to nothing of its
// per-key state. Before, every bucket stayed live beside its row until
// the response left the reducer: on the retention-shaped stage (15
// slots, ~1 KB of state and ~1.3 KB of row a key) the two peaked
// together. Measured on the retention stage's slots with components
// on (the per-group Components entries are built from each bucket
// before it is released) and off; held is what the partial keeps a key
// once the response exists, against the state it held before finalize.
func TestGroupMemory_FinalizeReleasesBuckets(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	for _, off := range []bool{false, true} {
		f := measureAggMemory(t, aggMemoryCase{aggs: retentionStageAggregations(), off: off, rows: 2, merge: true}, aggMemoryKeys)
		held := f.finalized - f.response
		t.Logf("components off=%-5v: merged state %7.1f B a key, response %7.1f, partial still holds %6.1f after finalize", off, f.merged, f.response, held)
		if held > 0.25*f.merged {
			t.Errorf("components off=%v: the merged partial still holds %.1f B a key after finalize (state %.1f): its buckets were not released", off, held, f.merged)
		}
	}
}
