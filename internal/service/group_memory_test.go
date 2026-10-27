package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"weak"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// groupMemoryCeiling is the resident accumulation state one group may
// cost on the grouped streaming arms (U51 E2-S4, #358, FR-25): the
// key's map slot, its bucket (aggregator slots + per-slot floors), the
// aggregators themselves, the key string and the grouper's live
// components entry.
const groupMemoryCeiling = 512

// groupMemoryKeys is the gated cardinality.
const groupMemoryKeys = 800_000

// groupMemoryRequest is the gated request: AGG_COUNT + AGG_SUM +
// AGG_AVERAGE grouped by one key, components on (the plan computes
// per-group floors).
func groupMemoryRequest() *types.Request {
	return &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "x", Label: "c"},
			{Type: types.AGG_SUM, Field: "x", Label: "s"},
			{Type: types.AGG_AVERAGE, Field: "y", Label: "a"},
		}}
}

// liveHeap is the live heap after two full collections.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

// groupMemoryPartition builds one empty partition for the gated request
// on svc, exactly as both parallel reducers build theirs
// (newPartitionPartial under the run's compute plan), plus a fold that
// routes one record per key in [from, to) through it — the reducers'
// per-row order (floor, then grouped fold).
func groupMemoryPartition(t *testing.T, svc *Service) (*shardPartial, func(from, to int)) {
	t.Helper()
	schema := groupedSumSchema()
	req := processing.StampWeightsWith(groupMemoryRequest(), nil, svc.extensions)
	specs, err := lookupAggSpecs(req, svc.extensions)
	if err != nil {
		t.Fatal(err)
	}
	factories, err := lookupGrouperFactories(req, svc.extensions)
	if err != nil {
		t.Fatal(err)
	}
	compute := svc.computePlanFor(context.Background(), req)
	if !compute.Aggs || !compute.Groups {
		t.Fatalf("compute plan %+v: the gate runs with components (per-group floors) on", compute)
	}
	p, err := newPartitionPartial(req, schema, svc.extensions, specs, factories, compute, nil, svc.Limits())
	if err != nil {
		t.Fatal(err)
	}
	rec := processing.NewRecord(schema, map[string]float64{"g": 0, "x": 1.5, "y": 2.25})
	fold := func(from, to int) {
		for k := from; k < to; k++ {
			rec.Set("g", float64(k))
			p.observeFloor(rec, specs)
			if err := p.foldGroupedRow(rec); err != nil {
				t.Fatal(err)
			}
		}
	}
	return p, fold
}

// TestGroupMemory_PartitionBytesPerGroup (U51 E2-S4, #358, FR-25): a
// grouped partition of the parallel reducers — the state the serial
// streaming-grouped scan keeps too (processing.GroupBucket) — holds at
// most groupMemoryCeiling bytes per group at 800,000 keys on the gated
// request, measured as live heap after a full GC (the partition alive,
// every per-row temporary collected). Before the trims it was ~866 B: an
// inline 128-byte WeightFloor in every slot's floor even unweighted, a
// second per-key floor table, and a key-order list.
func TestGroupMemory_PartitionBytesPerGroup(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	svc := New(nil)
	before := liveHeap()
	p, fold := groupMemoryPartition(t, svc)
	fold(0, groupMemoryKeys)
	after := liveHeap()
	if len(p.groups) != groupMemoryKeys {
		t.Fatalf("%d groups, want %d", len(p.groups), groupMemoryKeys)
	}
	perGroup := float64(after-before) / groupMemoryKeys
	t.Logf("partition: %.1f bytes/group at %d keys", perGroup, groupMemoryKeys)
	if perGroup > groupMemoryCeiling {
		t.Errorf("partition holds %.1f bytes/group, ceiling %d", perGroup, groupMemoryCeiling)
	}
	runtime.KeepAlive(p)
}

// TestGroupMemory_MergedBytesPerGroup: the merged state of four
// partitions — each folding a disjoint quarter of the 800,000 keys plus
// one key every partition shares — stays under the same ceiling once
// the absorbed partitions are released: a bucket moves into the merged
// map whole, and nothing of the source partition outlives its merge.
func TestGroupMemory_MergedBytesPerGroup(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	svc := New(nil)
	before := liveHeap()
	m := partialMerger{req: groupMemoryRequest()}
	const parts = 4
	for i := range parts {
		p, fold := groupMemoryPartition(t, svc)
		fold(i*groupMemoryKeys/parts, (i+1)*groupMemoryKeys/parts)
		fold(0, 1) // the shared key: a slot-wise merge, not a move
		if err := m.add(p); err != nil {
			t.Fatal(err)
		}
	}
	merged := m.result()
	after := liveHeap()
	if len(merged.groups) != groupMemoryKeys {
		t.Fatalf("%d merged groups, want %d", len(merged.groups), groupMemoryKeys)
	}
	if n := merged.groups["0"].Floors[0].N(); n != parts+1 {
		t.Fatalf("shared key's floor n = %d, want %d (one row per partition + its own)", n, parts+1)
	}
	perGroup := float64(after-before) / groupMemoryKeys
	t.Logf("merged: %.1f bytes/group at %d keys over %d partitions", perGroup, groupMemoryKeys, parts)
	if perGroup > groupMemoryCeiling {
		t.Errorf("merged state holds %.1f bytes/group, ceiling %d", perGroup, groupMemoryCeiling)
	}
	runtime.KeepAlive(merged)
}

// manyShardArchive writes a 40-shard archive whose every shard carries
// the same 16 tag keys (writeDictArchive), so each shard's partial holds
// a full bucket set and the merge folds key by key.
func manyShardArchive(t *testing.T) *fs.Config {
	t.Helper()
	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "many.pulse", 40, 50, 16)
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// manyShardRequest is grouped and mergeable, with an order-sensitive
// (Welford) slot and a value-state (mode) slot beside the binned sum.
func manyShardRequest() *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "many.pulse"},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tag"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "x0", Label: "s"},
			{Type: types.AGG_VARIANCE, Field: "x1", Label: "v"},
			{Type: types.AGG_MODE, Field: "tag", Label: "m"},
			{Type: types.AGG_COUNT, Field: "x2", Label: "c"},
		}}
}

// TestGroupMemory_LivePartialsBoundedByWindow (U51 E2-S4): on an archive
// of far more shards than workers, the per-shard reducer never holds
// more than limits.ShardMergeWindow(workers) unmerged partials, and a
// merged partial is released — checked by REACHABILITY, not by
// bookkeeping: every partial handed to the merge gets a weak pointer,
// and at each merge (after a full GC) the partials still alive besides
// the merged head are counted. Keeping every partial until one final
// merge (the pre-E2-S4 arm) leaves all of them alive.
func TestGroupMemory_LivePartialsBoundedByWindow(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate; swaps the package-level service.shardMergeHook test hook

	cfg := manyShardArchive(t)
	for _, workers := range []int{2, 3} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			window := limits.ShardMergeWindow(workers)
			var (
				ptrs     []weak.Pointer[shardPartial]
				maxAlive int
				merges   int
			)
			shardMergeHook = func(idx int, p *shardPartial) {
				if idx != len(ptrs) {
					t.Errorf("merge order: shard %d folded at position %d", idx, len(ptrs))
				}
				ptrs = append(ptrs, weak.Make(p))
				merges++
				runtime.GC()
				alive := 0
				for _, w := range ptrs[1:] { // ptrs[0] is the merged head
					if w.Value() != nil {
						alive++
					}
				}
				maxAlive = max(maxAlive, alive)
			}
			defer func() { shardMergeHook = nil }()
			svc := New(cfg)
			svc.SetShardWorkers(workers)
			if _, err := svc.Process(context.Background(), manyShardRequest()); err != nil {
				t.Fatal(err)
			}
			if merges != 40 {
				t.Fatalf("%d merges, want 40 (the run did not take the per-shard arm?)", merges)
			}
			// The partial being merged is alive (the hook holds it) on
			// top of at most window - 1 others still pending.
			t.Logf("max unmerged partials alive: %d (window %d, 40 shards)", maxAlive, window)
			if maxAlive > window {
				t.Errorf("%d partials alive at once, window %d", maxAlive, window)
			}
		})
	}
}

// TestGroupMemory_ManyShardsWorkerInvariant: with more shards than the
// dispatch window — so the throttle holds shards back and partials
// complete out of order — the answer (Data and Components, Welford and
// mode slots included) is identical under every worker count: the merge
// folds in shard index order whichever worker finishes first.
func TestGroupMemory_ManyShardsWorkerInvariant(t *testing.T) {
	t.Parallel()

	cfg := manyShardArchive(t)
	var want *types.Response
	for _, workers := range []int{2, 3, 5, 8} {
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		got, err := svc.Process(context.Background(), manyShardRequest())
		if err != nil {
			t.Fatal(err)
		}
		if got.Components == nil || got.Components.Run == nil || got.Components.Run.ShardCount != 40 {
			t.Fatalf("workers=%d: Components.Run %+v, want shard_count 40", workers, got.Components)
		}
		if want == nil {
			want = got
			continue
		}
		if !reflect.DeepEqual(got.Data, want.Data) || !reflect.DeepEqual(got.Components, want.Components) {
			t.Errorf("workers=%d: answer differs from workers=2", workers)
		}
	}
}

// TestGroupMemory_MergeWindowFitsBudget (U51 E5-S4): under a set
// MaxEstimatedMemory the per-shard reducer shrinks its merge window to
// the largest one whose whole-run estimate fits
// (limits.ShardMergeWindowWithin), and predict's rule
// (descx.ShardMergeWindow, whose estimate RequestLimitFindings judges)
// is the window the run takes — at every budget from the unshrunk
// estimate down to below one live partial. A run under a shrunk window
// answers byte-identically to the unbudgeted run (same merge order); a
// budget below one partial is refused on both sides with the same
// figure.
func TestGroupMemory_MergeWindowFitsBudget(t *testing.T) {
	// serial: swaps the package-level service.shardWindowHook test hook

	cfg := manyShardArchive(t)
	const workers = 3
	full := limits.ShardMergeWindow(workers)
	ctx := context.Background()
	open := func(budget int64) *Service {
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		l := limits.Defaults()
		l.MaxEstimatedMemory = budget
		svc.SetLimits(l)
		return svc
	}
	run := func(svc *Service) (resp *types.Response, window int, err error) {
		shardWindowHook = func(w int) { window = w }
		defer func() { shardWindowHook = nil }()
		resp, err = svc.Process(ctx, manyShardRequest())
		return resp, window, err
	}
	// The unbudgeted answer and the estimate at every window.
	base, w0, err := run(open(0))
	if err != nil || w0 != full {
		t.Fatalf("unbudgeted run: window %d (want %d), %v", w0, full, err)
	}
	probe := open(1)
	cohort := mustOpen(t, probe, "many.pulse")
	lin, err := probe.limitInputs(ctx, cohort, "many.pulse")
	if err != nil {
		t.Fatal(err)
	}
	estFull, ok := descx.EstimateMemory(manyShardRequest(), cohort.Schema(), probe.instance, lin)
	if !ok {
		t.Fatal("no estimate")
	}
	est1 := archiveMemoryEstimate(t, probe, manyShardRequest()) // refused at window 1
	if est1 >= estFull {
		t.Fatalf("one live partial (%d) costs no less than %d (%d)", est1, full, estFull)
	}
	mid := (est1 + estFull) / 2
	for _, c := range []struct {
		name   string
		budget int64
		window int // 0: refused
	}{
		{"unshrunk estimate", estFull, full},
		{"between", mid, -1},
		{"one partial", est1, 1},
		{"below one partial", est1 - 1, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc := open(c.budget)
			l := svc.Limits()
			predicted := descx.ShardMergeWindow(manyShardRequest(), cohort.Schema(), svc.instance, l, lin)
			findings := descx.RequestLimitFindings(manyShardRequest(), cohort.Schema(), svc.instance, l, lin)
			resp, window, err := run(svc)
			if c.window == 0 {
				d := requireCodedLimit(t, err)
				if len(findings) != 1 || d["observed"] != findings[0].Estimated || findings[0].Estimated != est1 {
					t.Fatalf("refusal observed %v, predict findings %+v, want estimate %d", d["observed"], findings, est1)
				}
				return
			}
			if err != nil || len(findings) != 0 {
				t.Fatalf("budget %d: run %v, predict findings %+v", c.budget, err, findings)
			}
			if window != predicted {
				t.Fatalf("budget %d: run window %d, predict window %d", c.budget, window, predicted)
			}
			if c.window > 0 && window != c.window || c.window < 0 && (window <= 1 || window >= full) {
				t.Fatalf("budget %d: window %d (full %d)", c.budget, window, full)
			}
			if !reflect.DeepEqual(resp.Data, base.Data) || !reflect.DeepEqual(resp.Components, base.Components) {
				t.Fatalf("budget %d (window %d): answer differs from the unbudgeted run", c.budget, window)
			}
		})
	}
}

// requireCodedLimit asserts err is PULSE_LIMIT_EXCEEDED and returns its
// details.
func requireCodedLimit(t *testing.T, err error) map[string]any {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("err %v, want PULSE_LIMIT_EXCEEDED", err)
	}
	return ce.Details
}
