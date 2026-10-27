package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Response.Components is keyed to the REQUEST, never to a worker count
// (skills/response-components.md, Concurrency knobs). The per-shard
// parallel reducer used to answer a grouped request with no `groupers`
// block, and to report `run.shard_count` that the serial arm over the
// same archive omitted — the same request returning two different
// Components shapes depending on Options.ShardWorkers.
//
// This pins the WHOLE {Data, Components} document of the serial arm
// (ShardWorkers=1) against the parallel arm (ShardWorkers=2,3) — per-group
// aggregation Components (groups[]) included — for every
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
		// A tally attribute (U51 E3-S2): each shard scores its rows on
		// its own instance; the merged out-of-range counts, the
		// attribute's Components.Attributes entry and its warning equal
		// the serial run's.
		"scale_score_category": {
			Cohort: &types.Cohort{Filename: path},
			Attributes: []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Fields: []string{"weight", "amount"}, Label: "sc",
				Params: json.RawMessage(`{"scale_min":0,"scale_max":30}`)}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: append(aggs(), &types.Aggregation{Type: types.AGG_SUM, Field: "sc", Label: "sum_sc"}),
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
						Warnings   any
					}{resp.Data, resp.Components, resp.Overlays, resp.Warnings})
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
				if len(req.Attributes) > 0 {
					ac := serial.Components.Attributes
					if len(ac) != 1 || ac[0].N == 0 || ac[0].Operator["n_out_of_range"] == 0 || len(serial.Warnings) == 0 {
						t.Fatalf("serial arm emitted a degenerate attribute tally %+v (warnings %d); parity against it proves nothing", ac, len(serial.Warnings))
					}
				}
				if len(req.Overlays) > 0 && len(serial.Overlays) == 0 {
					t.Fatal("serial arm emitted no overlay layer; parity against it proves nothing")
				}
				if len(req.Groups) > 0 {
					gc := serial.Components.Groupers
					if len(gc) != 1 || gc[0].TotalN == 0 || gc[0].Operator == nil {
						t.Fatalf("serial arm emitted a degenerate groupers block %+v; parity against it proves nothing", gc)
					}
					// Per-group aggregation Components ride every arm too.
					ac := serial.Components.Aggregations
					if len(ac) != len(req.Aggregations) || len(ac[0].Groups) == 0 || len(ac[0].Groups) != len(serial.Data) {
						t.Fatalf("serial arm emitted no per-group aggregation components (%d slots, Data %d rows); parity against it proves nothing", len(ac), len(serial.Data))
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
