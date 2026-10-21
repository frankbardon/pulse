package service

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// aggMemorySchema is the per-aggregator gate's cohort: the key g, the
// composite-key columns h and i, the numeric measures x / y / z, the
// weight w, a 4-member set field and 64 numeric vector members
// f00…f63.
func aggMemorySchema() *encoding.Schema {
	d := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := d.Add(v); err != nil {
			panic(err)
		}
	}
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeU32},
		{Name: "h", Type: encoding.FieldTypeU32},
		{Name: "i", Type: encoding.FieldTypeU32},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "z", Type: encoding.FieldTypeF64},
		{Name: "w", Type: encoding.FieldTypeF64},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: d},
	}}
	for i := range aggMemoryMaxMembers {
		s.Fields = append(s.Fields, encoding.Field{Name: fmt.Sprintf("f%02d", i), Type: encoding.FieldTypeF64})
	}
	off := 0
	for i := range s.Fields {
		s.Fields[i].ByteOffset = off
		s.Fields[i].CsvColumnIdx = i
		off += s.Fields[i].Type.ByteSize()
	}
	return s
}

// aggMemoryMaxMembers is the widest vector the gate measures.
const aggMemoryMaxMembers = 64

// aggMemorySlot is a valid slot of type at over aggMemorySchema; a
// vector aggregator reads the first members fNN columns.
func aggMemorySlot(at types.AggregationType, label string, members int) *types.Aggregation {
	a := &types.Aggregation{Type: at, Field: "x", Label: label}
	switch at {
	case types.AGG_FREQUENCY:
		a.Params = json.RawMessage(`{"value":"1"}`)
	case types.AGG_PERCENTILE:
		a.Params = json.RawMessage(`{"percentile":75}`)
	case types.AGG_WEIGHTED_MEAN:
		a.Params = json.RawMessage(`{"weight_field":"w"}`)
	case types.AGG_RATIO:
		a.Params = json.RawMessage(`{"numerator_field":"y","denominator_field":"z"}`)
	case types.AGG_DISTINCT_SUM:
		a.Params = json.RawMessage(`{"distinct_by":"y"}`)
	case types.AGG_MAPE, types.AGG_RMSE, types.AGG_MAE:
		a.Params = json.RawMessage(`{"prediction_field":"y"}`)
	case types.AGG_AUC:
		a.Params = json.RawMessage(`{"outcome":"z"}`)
	case types.AGG_VEC_MEAN, types.AGG_VEC_SUM, types.AGG_VEC_SD:
		a.Field = ""
		for i := range members {
			a.Fields = append(a.Fields, fmt.Sprintf("f%02d", i))
		}
	case types.AGG_SET_UNION, types.AGG_SET_INTERSECTION, types.AGG_SET_FREQUENCY,
		types.AGG_SET_CARDINALITY_SUM, types.AGG_SET_CARDINALITY_AVG, types.AGG_SET_DISTINCT_VALUES:
		a.Field = "tags"
	}
	return a
}

// aggMemoryCase is one measured request shape.
type aggMemoryCase struct {
	aggs     []*types.Aggregation
	weighted bool
	off      bool // components disabled
	rows     int  // rows folded per key per partition
	merge    bool // fold a second partition holding every key
	groupers int  // 1 (g) to 3 (g, h, i); 0 means 1
}

// aggMemoryFigures are the per-key live-heap figures of one case: a
// partition's state, the merged state of two partitions (= partition
// when !merge), the merged partial plus the finalized response (the
// partial kept reachable, as the reducer's frame keeps it while the
// response is built), and the response alone once the partial is
// dropped.
type aggMemoryFigures struct {
	partition, merged, finalized, response float64
}

// measureAggMemory builds the case's partitions exactly as the
// parallel reducers do (newPartitionPartial under the run's compute
// plan), folds rows rows per key into each, merges them in order
// (partialMerger) and finalizes (finalizeMergedPartial), reading the
// live heap after a full GC at each step.
func measureAggMemory(t *testing.T, c aggMemoryCase, keys int) aggMemoryFigures {
	t.Helper()
	svc := New(nil)
	svc.SetDisableComponents(c.off)
	schema := aggMemorySchema()
	req := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}}
	for _, f := range []string{"h", "i"}[:max(c.groupers, 1)-1] {
		req.Groups = append(req.Groups, &types.Group{Type: types.GROUP_CATEGORY, Field: f})
	}
	for _, a := range c.aggs {
		cp := *a
		req.Aggregations = append(req.Aggregations, &cp)
	}
	if c.weighted {
		req.Weight = &types.WeightSpec{Field: "w"}
	}
	req = processing.StampWeightsWith(req, nil, svc.extensions)
	specs, err := lookupAggSpecs(req, svc.extensions)
	if err != nil {
		t.Fatal(err)
	}
	factories, err := lookupGrouperFactories(req, svc.extensions)
	if err != nil {
		t.Fatal(err)
	}
	compute := svc.computePlanFor(context.Background(), req)
	if compute.Groups == c.off {
		t.Fatalf("compute plan %+v under components off=%v", compute, c.off)
	}
	vals := map[string]float64{"g": 0, "x": 1.5, "y": 2.25, "z": 1, "w": 1.25}
	for i := range aggMemoryMaxMembers {
		vals[fmt.Sprintf("f%02d", i)] = float64(i) + 0.5
	}
	rec := processing.NewRecordWithWide(schema, vals, nil, map[string]any{"tags": uint64(0b1011)})
	perPart := keys * c.rows
	part := func(shard int) *shardPartial {
		p, err := newPartitionPartial(req, schema, svc.extensions, specs, factories, compute, nil, svc.Limits())
		if err != nil {
			t.Fatal(err)
		}
		for k := range keys {
			for j := range c.rows {
				rec.Set("g", float64(k))
				rec.Set("h", float64(k%3))
				rec.Set("i", float64(k%5))
				// Distinct values per key (and per partition): the
				// value-state aggregators keep one entry each.
				rec.Set("x", float64(k%97)+float64(shard*c.rows+j)*0.25)
				rec.Set("y", float64(shard*c.rows+j)+0.75)
				rec.SetMergePosition(shard, shard*perPart, k*c.rows+j)
				p.observeFloor(rec, specs)
				if err := p.foldGroupedRow(rec); err != nil {
					t.Fatal(err)
				}
			}
		}
		return p
	}
	before := liveHeap()
	m := partialMerger{req: req}
	if err := m.add(part(0)); err != nil {
		t.Fatal(err)
	}
	afterPart := liveHeap()
	if c.merge {
		if err := m.add(part(1)); err != nil {
			t.Fatal(err)
		}
	}
	merged := m.result()
	m = partialMerger{}
	afterMerge := liveHeap()
	resp, err := finalizeMergedPartial(req, schema, merged, 0, compute, svc.extensions, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != keys {
		t.Fatalf("%d rows, want %d", len(resp.Data), keys)
	}
	afterFinal := liveHeap()
	runtime.KeepAlive(merged)
	merged = nil
	afterDrop := liveHeap()
	runtime.KeepAlive(merged)
	runtime.KeepAlive(resp)
	n := float64(keys)
	return aggMemoryFigures{
		partition: float64(afterPart-before) / n,
		merged:    float64(afterMerge-before) / n,
		finalized: float64(afterFinal-before) / n,
		response:  float64(afterDrop-before) / n,
	}
}

// aggMemoryKeys is the gated cardinality: enough keys that a byte a
// key is resolved, few enough to keep the sweep fast.
const aggMemoryKeys = 10_000

// vectorAggregation reports whether at is a built-in AGG_VEC_*.
func vectorAggregation(at types.AggregationType) bool {
	return at == types.AGG_VEC_MEAN || at == types.AGG_VEC_SUM || at == types.AGG_VEC_SD
}

// aggMemoryBufferedRows is the rows the gate's second partition (shard
// 1, starting at record perPart) holds in its non-head fragment: the
// rest of the merge block the first partition ends inside.
func aggMemoryBufferedRows(perPart int) int64 {
	if off := perPart % limits.MergeBlockSize; off != 0 {
		return int64(min(limits.MergeBlockSize-off, perPart))
	}
	return 0
}

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
	if testing.Short() {
		t.Skip("live-heap sweep over every built-in aggregator")
	}
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

// TestGroupMemory_VectorBufferedRows (U51 E5-S7): a shard that starts
// inside a merge block keeps that block's rows in a non-head fragment
// until finalize; each costs at most limits.VectorAggregationBytes' row
// figure, at 3, 16 and 64 members, weighted (the widest row).
func TestGroupMemory_VectorBufferedRows(t *testing.T) {
	const rows = limits.MergeBlockSize - 1
	schema := aggMemorySchema()
	vals := map[string]float64{"w": 1.25}
	for i := range aggMemoryMaxMembers {
		vals[fmt.Sprintf("f%02d", i)] = float64(i) + 0.5
	}
	for _, at := range []types.AggregationType{types.AGG_VEC_MEAN, types.AGG_VEC_SD} {
		for _, p := range []int{3, 16, 64} {
			svc := New(nil)
			req := processing.StampWeightsWith(&types.Request{Aggregations: []*types.Aggregation{aggMemorySlot(at, "a", p)},
				Weight: &types.WeightSpec{Field: "w"}}, nil, svc.extensions)
			specs, err := lookupAggSpecs(req, svc.extensions)
			if err != nil {
				t.Fatal(err)
			}
			part, err := newPartitionPartial(req, schema, svc.extensions, specs, nil, svc.computePlanFor(context.Background(), req), nil, svc.Limits())
			if err != nil {
				t.Fatal(err)
			}
			rec := processing.NewRecordWithWide(schema, vals, nil, map[string]any{"tags": uint64(1)})
			before := liveHeap()
			for r := range rows {
				// Shard 1 starts one record into block 0: every row is
				// a non-head fragment's.
				rec.SetMergePosition(1, 1, r)
				if err := part.aggs[0].UpdateRow(rec, ""); err != nil {
					t.Fatal(err)
				}
			}
			after := liveHeap()
			got := float64(after-before) / rows
			bound := float64(limits.VectorAggregationBytes(at, int64(p), 1, rows)) / rows
			t.Logf("%s p=%d: %.1f B a buffered row, charged %.1f", at, p, got, bound)
			if got > bound {
				t.Errorf("%s p=%d: a buffered row holds %.1f B, charged %.1f", at, p, got, bound)
			}
			runtime.KeepAlive(part)
		}
	}
}

// aggMemoryCharge is the memory model's per-key charge for case c over
// keys keys: one merged bucket's state and finalize output
// (limits.BucketBytes — the components-off output when c.off), the
// vector aggregators' fragments and buffered rows spread over the keys,
// and every value-state slot's limits.ValueEntryBytes a record. The
// figures descx.EstimateMemory charges a request of this shape per
// bucket.
func aggMemoryCharge(c aggMemoryCase, keys int) (state, final float64) {
	mi := limits.MemoryInputs{Aggregations: int64(len(c.aggs)), ComponentsOff: c.off, ExtraGroupers: int64(max(c.groupers, 1) - 1)}
	parts := 1
	if c.merge {
		parts = 2
	}
	records := int64(parts * c.rows * keys)
	var perKey float64
	for _, a := range c.aggs {
		p := int64(len(a.Fields))
		mi.AggStateBytes += limits.AggregationStateBytes(a.Type, c.weighted, p)
		mi.AggOutputOffBytes += limits.AggregationOutputOffBytes(a.Type, p)
		if a.Type == types.AGG_SET_FREQUENCY {
			mi.SetMembers += 4
		}
		var buffered int64
		if c.merge {
			buffered = aggMemoryBufferedRows(keys * c.rows)
		}
		perKey += float64(limits.VectorAggregationBytes(a.Type, p, int64(parts*keys), buffered)) / float64(keys)
		if descx.ValueStateAggregation(a.Type) {
			perKey += float64(records*limits.ValueEntryBytes) / float64(keys)
		}
	}
	s, f := limits.BucketBytes(mi)
	return float64(s) + perKey, float64(f) + perKey
}

// retentionStageAggregations is the retention chain's per-customer
// stage (BenchmarkRetentionShape): AGG_SUM, AGG_DISTINCT_COUNT, AGG_MIN
// and twelve AGG_MAX.
func retentionStageAggregations() []*types.Aggregation {
	aggs := []*types.Aggregation{
		aggMemorySlot(types.AGG_SUM, "spend", 0),
		aggMemorySlot(types.AGG_DISTINCT_COUNT, "orders", 0),
		aggMemorySlot(types.AGG_MIN, "first_month", 0),
	}
	for m := range 12 {
		aggs = append(aggs, aggMemorySlot(types.AGG_MAX, fmt.Sprintf("active_m%02d", m+1), 0))
	}
	return aggs
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
	if testing.Short() {
		t.Skip("live-heap sweep over finalized rows")
	}
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
	if testing.Short() {
		t.Skip("live-heap sweep over finalized rows")
	}
	for _, off := range []bool{false, true} {
		f := measureAggMemory(t, aggMemoryCase{aggs: retentionStageAggregations(), off: off, rows: 2, merge: true}, aggMemoryKeys)
		held := f.finalized - f.response
		t.Logf("components off=%-5v: merged state %7.1f B a key, response %7.1f, partial still holds %6.1f after finalize", off, f.merged, f.response, held)
		if held > 0.25*f.merged {
			t.Errorf("components off=%v: the merged partial still holds %.1f B a key after finalize (state %.1f): its buckets were not released", off, held, f.merged)
		}
	}
}
