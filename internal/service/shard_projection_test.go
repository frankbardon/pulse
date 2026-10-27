package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// shard_projection_test.go pins U51 E2-S2 (#356): the per-shard parallel
// reducer decodes through the request's projection — one DecodePlan and
// one projected record binding built per run — instead of decoding
// every field of every row.

// TestShardParallel_ProjectionTransparent: projection is output-
// transparent on the per-shard parallel arm. The whole {Data,
// Components, Overlays} document with projection on equals the one with
// projection off, over a FLAT (0x01) archive and its GROUPED (0x02) twin
// — parent-group members, a set member, filters on a member and on a row
// field — and equals the serial projected arm's.
//
// Falsified by narrowing decodeProjection's keep filter to drop one
// needed field (the projected answer then differs).
func TestShardParallel_ProjectionTransparent(t *testing.T) {
	t.Parallel()

	shards := parityShards()
	files := map[string][]byte{}
	var flatNames, groupedNames []string
	for i, g := range shards {
		flat, grouped, _ := g.build(t, nil)
		fn, gn := fmt.Sprintf("f/s%d.pulse", i), fmt.Sprintf("g/s%d.pulse", i)
		files[fn], files[gn] = flat, grouped
		flatNames, groupedNames = append(flatNames, fn), append(groupedNames, gn)
	}
	svc, _ := gEnv(t, files)
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "flat.pulse", flatNames); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateShardArchive(ctx, "grouped.pulse", groupedNames); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"flat.pulse", "grouped.pulse"} {
		cohort, err := svc.Open(ctx, arch)
		if err != nil {
			t.Fatal(err)
		}
		if arch == "grouped.pulse" && !cohort.Schema().HasGroups() {
			t.Fatal("the grouped archive carries no parent groups; the 0x02 arm would go untested")
		}
		reqs := parityRequests(arch)
		reqs["filter_on_member"] = &types.Request{
			Cohort:       &types.Cohort{Filename: arch},
			Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"west", "north"}}},
			Groups:       []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "tags"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "amt"}},
		}
		for name, req := range reqs {
			t.Run(arch+"/"+name, func(t *testing.T) {
				answer := func(workers int, project bool) string {
					svc.SetShardWorkers(workers)
					svc.SetProjectBufferedFields(project)
					defer svc.SetShardWorkers(0)
					defer svc.SetProjectBufferedFields(false)
					if workers != 1 {
						if _, ok := svc.shouldFanOut(req, cohort); !ok {
							t.Fatalf("shouldFanOut refused %d workers", workers)
						}
					}
					if project && svc.shardDecodeFor(ctx, req, cohort.Schema()).plan == nil {
						t.Fatal("the request builds no projection; the comparison would be vacuous")
					}
					resp, err := svc.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process(workers=%d, project=%v): %v", workers, project, err)
					}
					b, err := json.Marshal(struct {
						Data       any
						Components any
						Overlays   any
					}{resp.Data, resp.Components, resp.Overlays})
					if err != nil {
						t.Fatal(err)
					}
					return string(b)
				}
				want := answer(3, false)
				if got := answer(3, true); got != want {
					t.Errorf("projected shard arm differs from full decode\n got  %s\n want %s", got, want)
				}
				if got := answer(1, true); got != want {
					t.Errorf("serial projected arm differs from the parallel full decode\n got  %s\n want %s", got, want)
				}
			})
		}
	}
}

// TestShardParallel_ProjectedDecodeAllocatesLess: over a p = 50 archive
// a one-column request allocates a fraction of the full decode's bytes
// on the per-shard parallel arm — the record is bound to the retained
// field and the plan skips the other 49 — where before the arm decoded
// every field whatever the request read.
//
// Falsified by building shardDecodeFor with a nil plan and keep (the
// full decode): both runs then allocate alike.
func TestShardParallel_ProjectedDecodeAllocatesLess(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const p, rows = 50, 4 * linalg.MergeBlockSize
	dir := t.TempDir()
	path := matrixScaleCohort(t, dir, rows, p, 4)
	svc := New(matrixScaleOSConfig(t, dir))
	svc.SetShardWorkers(4)
	req := &types.Request{Cohort: &types.Cohort{Filename: path}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x000", Label: "s"}}}
	if _, ok := svc.shouldFanOut(req, mustOpen(t, svc, path)); !ok {
		t.Fatal("the request does not take the per-shard parallel arm")
	}
	if !processing.CanMergeRequest(req, mustOpen(t, svc, path).Schema()) {
		t.Fatal("request is not mergeable")
	}
	run := func(project bool) uint64 {
		svc.SetProjectBufferedFields(project)
		// Warm once so one-time caches (layouts, plans) do not count.
		if _, err := svc.Process(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		return totalAlloc(t, func() error {
			_, err := svc.Process(context.Background(), req)
			return err
		})
	}
	full, projected := run(false), run(true)
	t.Logf("full decode %.1f B/row, projected %.1f B/row", float64(full)/rows, float64(projected)/rows)
	if projected*2 > full {
		t.Fatalf("projected run allocates %d bytes, full decode %d: want under half (the arm is not projecting)", projected, full)
	}
}

// TestShardStream_AnchoredShardStreamsSection: a stored shard opened
// through the anchor form (`archive.pulse#shard.pulse`) is scanned
// through a bounded window over its section of the archive — the
// per-shard reducer's read — never copied whole into memory first (the
// streaming iterator's afero.ReadFile fallback did). A streamed sum over
// a ~1.6 MB shard allocates well under the shard's bytes, with
// observability's byte counter on (whose wrapper must not change the
// read) and off.
//
// Falsified by deleting the anchorSectionSize branch in
// streamingIterator.initFromFile.
func TestShardStream_AnchoredShardStreamsSection(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const p, rows = 50, 4 * linalg.MergeBlockSize
	dir := t.TempDir()
	path := matrixScaleCohort(t, dir, rows, p, 4)
	svc := New(matrixScaleOSConfig(t, dir))
	shard := mustOpen(t, svc, path).Shards()[1]
	anchor := path + "#" + shard.Filename
	shardBytes := shard.RecordCount * int64(mustOpen(t, svc, anchor).Schema().RecordByteSize())
	req := &types.Request{Cohort: &types.Cohort{Filename: anchor}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x000", Label: "s"}}}
	for _, observed := range []bool{false, true} {
		t.Run(fmt.Sprintf("observed=%v", observed), func(t *testing.T) {
			ctx := context.Background()
			var ei *ExecInfo
			if observed {
				ei = &ExecInfo{}
				ctx = WithExecInfo(ctx, ei)
			}
			var resp *types.Response
			got := totalAlloc(t, func() error {
				var err error
				resp, err = svc.Process(ctx, req)
				return err
			})
			if resp.Metadata == nil || resp.Metadata.TotalRows != shard.RecordCount {
				t.Fatalf("anchored run scanned %+v rows, want %d", resp.Metadata, shard.RecordCount)
			}
			t.Logf("anchored shard of %d bytes: run allocates %d", shardBytes, got)
			if got*2 > uint64(shardBytes) {
				t.Fatalf("anchored run allocates %d bytes, want under half the %d-byte shard (the shard was read whole)", got, shardBytes)
			}
			if observed && ei.Snapshot().BytesRead < shardBytes {
				t.Errorf("bytes_read = %d, want >= the shard's %d record bytes", ei.Snapshot().BytesRead, shardBytes)
			}
		})
	}
}
