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
// key is resolved (the measured shares are whole size-class multiples,
// within ~1 B a key of their 10,000-key figures, against a tightest
// entry headroom of 16 B), and past limits.MergeBlockSize so the first
// partition ends inside a merge block at 1, 2 and 4 rows a key and the
// second buffers that block's rest (aggMemoryBufferedRows) — few
// enough to keep the sweep fast.
const aggMemoryKeys = 5_000

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

// TestGroupMemory_VectorBufferedRows (U51 E5-S7): a shard that starts
// inside a merge block keeps that block's rows in a non-head fragment
// until finalize; each costs at most limits.VectorAggregationBytes' row
// figure, at 3, 16 and 64 members, weighted (the widest row).
func TestGroupMemory_VectorBufferedRows(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

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
