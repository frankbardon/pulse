package service

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// extremumMemoryCeiling is the resident bytes one group may cost on the
// grouped streaming arms for extremumMemoryRequest (twelve AGG_MAX slots
// and one AGG_MIN, components off — the shape of a month-flag retention
// rollup): the key's map slot and string, the GroupBucket and its
// aggregator slots, and the slots' running state. Before memory-bounds
// E2-S2 every AGG_MAX / AGG_MIN slot was its own 24-byte heap object
// (running value, seen flag and a frozen mirror): ~650 B a group here.
// The 8-byte extremum state, minted for a bucket's slots in one slab,
// holds it at ~420 B.
const extremumMemoryCeiling = 480

// extremumMemoryKeys is the gated cardinality (kept small enough for
// the fast tier's 1 s budget).
const extremumMemoryKeys = 200_000

func extremumMemoryRequest() *types.Request {
	off := true
	req := &types.Request{
		Groups:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		DisableComponents: &off,
	}
	for i := range 12 {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MAX, Field: "x", Label: fmt.Sprintf("m%02d", i)})
	}
	req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MIN, Field: "y", Label: "lo"})
	return req
}

// TestGroupMemory_ExtremumBytesPerGroup (memory-bounds E2-S2): a grouped
// partition of the parallel reducers (the state the serial streaming
// scan keeps too) holding twelve AGG_MAX slots and one AGG_MIN costs at
// most extremumMemoryCeiling bytes a group, measured as live heap after
// a full GC with the partition alive.
func TestGroupMemory_ExtremumBytesPerGroup(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	svc := New(nil)
	schema := groupedSumSchema()
	req := processing.StampWeightsWith(extremumMemoryRequest(), nil, svc.extensions)
	specs, err := lookupAggSpecs(req, svc.extensions)
	if err != nil {
		t.Fatal(err)
	}
	factories, err := lookupGrouperFactories(req, svc.extensions)
	if err != nil {
		t.Fatal(err)
	}
	compute := svc.computePlanFor(context.Background(), req)
	before := liveHeap()
	p, err := newPartitionPartial(req, schema, svc.extensions, specs, factories, compute, nil, svc.Limits())
	if err != nil {
		t.Fatal(err)
	}
	rec := processing.NewRecord(schema, map[string]float64{"g": 0, "x": 1.5, "y": 2.25})
	for k := range extremumMemoryKeys {
		rec.Set("g", float64(k))
		p.observeFloor(rec, specs)
		if err := p.foldGroupedRow(rec); err != nil {
			t.Fatal(err)
		}
	}
	after := liveHeap()
	if len(p.groups) != extremumMemoryKeys {
		t.Fatalf("%d groups, want %d", len(p.groups), extremumMemoryKeys)
	}
	perGroup := float64(after-before) / extremumMemoryKeys
	t.Logf("partition: %.1f bytes/group at %d keys (13 extremum slots, components off)", perGroup, extremumMemoryKeys)
	if perGroup > extremumMemoryCeiling {
		t.Errorf("partition holds %.1f bytes/group, ceiling %d", perGroup, extremumMemoryCeiling)
	}
	runtime.KeepAlive(p)
}
