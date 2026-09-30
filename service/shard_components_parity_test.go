package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Response.Components is keyed to the REQUEST, never to a worker count
// (skills/response-components.md, Concurrency knobs). The per-shard
// parallel reducer used to answer a grouped request with no `groupers`
// block, and to report `run.shard_count` that the serial arm over the
// same archive omitted — the same request returning two different
// Components shapes depending on Options.ShardWorkers.
//
// This pins the WHOLE {Data, Components} document of the serial arm
// (ShardWorkers=1) against the parallel arm (ShardWorkers=2,3) for every
// mergeable grouper the shard fixture can carry plus the ungrouped
// request, over a FLAT (0x01) archive and its GROUPED (0x02) twin.

var parityTags = []string{"t0", "t1", "t2", "t3", "t4", "t5"}

// parityShards returns three shards with overlapping parents (so a
// grouper bucket is fed by more than one shard and a per-bucket count
// only comes out right if the merge sums it) and a set member.
func parityShards() []gShard {
	// tags is a member of the parent group, so it is a function of the
	// parent alone — identical in every shard that carries the parent.
	withTags := func(g gShard) gShard {
		g.tagRung, g.tagDict = encoding.FieldTypeSetU8, parityTags
		g.tagsOf = func(p int) []string {
			if p%11 == 0 {
				return nil // the empty mask: answered, selected nothing
			}
			return []string{parityTags[p%6], parityTags[(p/3)%6]}
		}
		return g
	}
	c := gShard{lo: 40, hi: 52, fanout: 5, idBase: 20000, regionDict: gRegions, src: "batch-a"}
	return []gShard{withTags(gShardA()), withTags(gShardB()), withTags(c)}
}

func parityRequests(path string) map[string]*types.Request {
	aggs := func() []*types.Aggregation {
		return []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
			{Type: types.AGG_SUM, Field: "amount", Label: "amt"},
			{Type: types.AGG_MAX, Field: "weight", Label: "wmax"},
		}
	}
	grouped := func(g *types.Group) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: path}, Groups: []*types.Group{g}, Aggregations: aggs()}
	}
	return map[string]*types.Request{
		"flat":                  {Cohort: &types.Cohort{Filename: path}, Aggregations: aggs()},
		"GROUP_CATEGORY":        grouped(&types.Group{Type: types.GROUP_CATEGORY, Field: "region"}),
		"GROUP_CATEGORY_incl":   grouped(&types.Group{Type: types.GROUP_CATEGORY, Field: "region", Include: []string{"west", "north"}}),
		"GROUP_RANGE":           grouped(&types.Group{Type: types.GROUP_RANGE, Field: "weight", Interval: 3}),
		"GROUP_SET_VALUE":       grouped(&types.Group{Type: types.GROUP_SET_VALUE, Field: "tags"}),
		"GROUP_SET_PER_ELEMENT": grouped(&types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}),
		"sorted_category": {
			Cohort:       &types.Cohort{Filename: path},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: aggs(),
			Sort:         []types.OrderKey{{Field: "n", Desc: true}},
		},
		"overlay_category": {
			Cohort:       &types.Cohort{Filename: path},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: aggs(),
			Overlays:     []types.OverlaySpec{{Name: "share", Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup}},
		},
		// A NON-streamable kind: the serial arm takes the buffered path
		// for it (canStreamOverlays), the parallel arm folds it at the
		// shared streaming tail — the answers must still agree.
		"overlay_sibling_category": {
			Cohort:       &types.Cohort{Filename: path},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: aggs(),
			Overlays: []types.OverlaySpec{{Name: "vs_north", Kind: types.OverlayKindIndexVsSibling, Scope: types.OverlayScopeGroup,
				Ref: types.OverlayRef{Sibling: &types.OverlaySiblingRef{Field: "region", Value: "north"}}}},
		},
		"filtered_category": {
			Cohort:       &types.Cohort{Filename: path},
			Filterers:    []*types.Filterer{{Type: types.FILTER_RANGE, Field: "weight", Values: []string{"12", "100"}}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "src"}},
			Aggregations: aggs(),
		},
	}
}

func TestShardWorkers_ComponentsParity_GroupedAndFlatArchives(t *testing.T) {
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
		t.Fatalf("CreateShardArchive(flat): %v", err)
	}
	if _, err := svc.CreateShardArchive(ctx, "grouped.pulse", groupedNames); err != nil {
		t.Fatalf("CreateShardArchive(grouped): %v", err)
	}

	for _, arch := range []string{"flat.pulse", "grouped.pulse"} {
		cohort, err := svc.Open(ctx, arch)
		if err != nil {
			t.Fatal(err)
		}
		if arch == "grouped.pulse" && !cohort.Schema().HasGroups() {
			t.Fatal("the grouped archive carries no parent groups; the 0x02 arm would go untested")
		}
		for name, req := range parityRequests(arch) {
			t.Run(arch+"/"+name, func(t *testing.T) {
				if !processing.CanMergeRequest(req, cohort.Schema()) {
					t.Fatal("request is not mergeable; the shard reducer would never fan out")
				}
				answer := func(workers int) (string, *types.Response) {
					svc.SetShardWorkers(workers)
					defer svc.SetShardWorkers(0)
					if workers != 1 {
						if _, ok := svc.shouldFanOut(req, cohort); !ok {
							t.Fatalf("shouldFanOut refused %d workers; this would compare serial against serial", workers)
						}
					}
					resp, err := svc.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process(workers=%d): %v", workers, err)
					}
					b, err := json.Marshal(struct {
						Data       any
						Components any
						Overlays   any
					}{resp.Data, resp.Components, resp.Overlays})
					if err != nil {
						t.Fatal(err)
					}
					return string(b), resp
				}
				want, serial := answer(1)
				if serial.Components == nil || serial.Components.Run == nil {
					t.Fatal("serial arm emitted no Components.Run")
				}
				if serial.Components.Run.ShardCount != len(shards) {
					t.Errorf("serial run.shard_count = %d, want %d (the cohort resolved to a %d-shard archive)",
						serial.Components.Run.ShardCount, len(shards), len(shards))
				}
				if len(req.Overlays) > 0 && len(serial.Overlays) == 0 {
					t.Fatal("serial arm emitted no overlay layer; parity against it proves nothing")
				}
				if len(req.Groups) > 0 {
					gc := serial.Components.Groupers
					if len(gc) != 1 || gc[0].TotalN == 0 || gc[0].Operator == nil {
						t.Fatalf("serial arm emitted a degenerate groupers block %+v; parity against it proves nothing", gc)
					}
				}
				for _, workers := range []int{2, 3} {
					if got, _ := answer(workers); got != want {
						t.Errorf("ShardWorkers=%d answers differently from ShardWorkers=1\n got  %s\n want %s", workers, got, want)
					}
				}

				// The opt-out holds on both arms: Components stays nil, not
				// a groupers-only shell from the shared grouped tail.
				off := true
				req.DisableComponents = &off
				defer func() { req.DisableComponents = nil }()
				for _, workers := range []int{1, 2} {
					if _, resp := answer(workers); resp.Components != nil {
						t.Errorf("DisableComponents ignored at ShardWorkers=%d: %+v", workers, resp.Components)
					}
				}
			})
		}
	}
}

// The DecodeWorkers arm shares shardPartial / foldGroupedRow /
// finalizeMergedPartial with the shard reducer, so it had the same
// grouped holes (no groupers block, Sort and include order dropped,
// GROUP_SET_PER_ELEMENT refused). Same whole-document parity, single
// file above the parallel-decode threshold.
func TestDecodeWorkers_GroupedAnswersParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping decode-worker grouped parity in -short mode")
	}
	const rowCount = parallelDecodeRecordThreshold + 4096
	schema := componentsParitySchema()
	recs, nullAt := componentsParityRows(rowCount, 0)
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/grouped_parity.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	aggs := []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "id", Label: "n"},
		{Type: types.AGG_SUM, Field: "score", Label: "sum"},
	}
	reqs := map[string]*types.Request{
		"range_sorted": {Cohort: &types.Cohort{Filename: path}, Aggregations: aggs,
			Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "score", Interval: 20000}},
			Sort:   []types.OrderKey{{Field: "n", Desc: true}}},
		"set_value":       {Cohort: &types.Cohort{Filename: path}, Aggregations: aggs, Groups: []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "picks"}}},
		"set_per_element": {Cohort: &types.Cohort{Filename: path}, Aggregations: aggs, Groups: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "picks"}}},
	}
	answer := func(t *testing.T, workers int, req *types.Request) string {
		t.Helper()
		svc := New(cfg)
		svc.SetDecodeWorkers(workers)
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process(DecodeWorkers=%d): %v", workers, err)
		}
		if resp.Components == nil || len(resp.Components.Groupers) != 1 {
			t.Fatalf("DecodeWorkers=%d emitted no groupers block: %+v", workers, resp.Components)
		}
		b, _ := json.Marshal(struct {
			Data       any
			Components any
		}{resp.Data, resp.Components})
		return string(b)
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}
	for name, req := range reqs {
		t.Run(name, func(t *testing.T) {
			if !processing.CanMergeRequest(req, schema) {
				t.Fatal("request is not mergeable; the parallel decode reducer would never engage")
			}
			want := answer(t, 1, req)
			if got := answer(t, 4, req); got != want {
				t.Errorf("DecodeWorkers=4 answers differently from DecodeWorkers=1\n got  %.600s\n want %.600s", got, want)
			}
		})
	}
}
