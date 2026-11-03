package service

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// weight_invariance_test.go is the weight family on the shared
// worker-invariance harness: weighted requests answer the same whole
// response (LayoutFree) whatever the knobs —
//
//   - the weighted core aggregators (weighting-descriptive E1-S3): the
//     per-shard and per-segment reducers build their aggregators off the
//     stamped spec and merge the weighted floor, the invalid-row tally
//     and the one PULSE_WEIGHT_INVALID_ROWS warning;
//   - an ungrouped AGG_WEIGHTED_MEAN, whose four weighted-moment
//     component keys the pairwise z overlay reads, Chan-merged per shard;
//   - the weighted two-means z overlay crosstab (AGG_WEIGHTED_MEAN cell)
//     on the fused and the buffered arm — an archive crosstab walks its
//     shards serially into one fold, and DecodeWorkers only
//     materialises the buffered arm's records in order.
//
// The fast tier runs the archive groups and the core aggregators'
// parallel-decode group (TestWeightInvariance_BigFile); the crosstab's
// parallel-decode group is TestWeightInvariance_CrosstabHeavy (heavy
// tier).

// weightInvCohorts is the weighted-core archive (90 / 77 / 101 rows)
// and its single-file twin.
func weightInvCohorts(t *testing.T) map[invariance.Layout]svcInvCohort {
	t.Helper()
	shards := []int{90, 77, 101}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "w.pulse", weightModesArchive(t, shards), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, nullAt := weightModesRows(90+77+101, 0)
	if err := afero.WriteFile(cfg.Fs(), "twin.pulse", writeNullablePulse(t, weightModesSchema(), recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	return map[invariance.Layout]svcInvCohort{invariance.File: {cfg, "twin.pulse"}, invariance.Archive: {cfg, "w.pulse"}}
}

// weightInvMergeTol is the Welford-merge bound the weighted reducers
// document (1e-12 relative): a shard or segment partition Chan-merges
// its partial moments, so mean / variance / m2 move by a few ULPs from
// the serial fold while every count, the weighted floor's invalid tally
// and the warning stay exact. The crosstab fold never merges partials
// and is compared bit for bit.
const weightInvMergeTol = 1e-12

// weightInvFamily runs mk over cohorts (setup per cell) under rule (nil
// = BitExact); check guards the reference.
func weightInvFamily(name string, cohorts map[invariance.Layout]svcInvCohort, groups []invariance.Group, mk func() *types.Request, setup func(*Service, invariance.Knobs), rule invariance.Rule, check func(t *testing.T, resp *types.Response)) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:    name,
		Groups:  groups,
		Compare: rule,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			c, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			var s func(*Service)
			if setup != nil {
				s = func(svc *Service) { setup(svc, k) }
			}
			return svcInvProcess(t, c, k, mk(), s)
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) { check(t, resp) },
	}
}

// weightInvCore is weightModesRequest without its cohort.
func weightInvCore() *types.Request { return weightModesRequest("") }

// weightInvMoments is the ungrouped AGG_WEIGHTED_MEAN over the
// weighted-z fixture.
func weightInvMoments() *types.Request {
	return &types.Request{Aggregations: []*types.Aggregation{{
		Type: types.AGG_WEIGHTED_MEAN, Field: "value", Label: "wmean",
		Params: json.RawMessage(`{"weight_field":"weight"}`),
	}}}
}

// weightInvFusion forces the buffered crosstab arm on a Buffered cell.
func weightInvFusion(svc *Service, k invariance.Knobs) {
	svc.SetDisableCrosstabFusion(k.Mode == invariance.Buffered)
}

// weightInvCrosstabCheck guards the crosstab reference: every overlay
// layer carries a p-value and every cell the weighted-moment keys.
func weightInvCrosstabCheck(t *testing.T, resp *types.Response) {
	t.Helper()
	wzsAssertNonVacuous(t, resp, len(wzsCrosstabRequest("").Overlays))
}

// TestWeightInvariance_Archive is the fast-tier half of the weight
// family: the three weighted requests on their shard archives against
// the single-file twins.
func TestWeightInvariance_Archive(t *testing.T) {
	t.Parallel()

	core := weightInvCohorts(t)
	if !processing.CanMergeRequest(processing.StampWeights(weightInvCore(), nil), weightModesSchema()) {
		t.Fatal("weighted fixture request is not mergeable")
	}
	invariance.Run(t, weightInvFamily("core", core, []invariance.Group{invariance.ArchiveGroup()}, weightInvCore, nil, invariance.Within(weightInvMergeTol, 0), assertWeightedUseful))

	wz := fs.NewMemMap()
	wzsArchive(t, wz, "wz.pulse", []int{37, 29, 41})
	wzCohorts := map[invariance.Layout]svcInvCohort{invariance.File: {wz, "wz.pulse.concat"}, invariance.Archive: {wz, "wz.pulse"}}
	if !processing.CanMergeRequest(weightInvMoments(), wzsSchema()) {
		t.Fatal("moments request is not mergeable; the shard reducer would never fan out")
	}
	invariance.Run(t, weightInvFamily("moments", wzCohorts, []invariance.Group{invariance.ArchiveGroup()}, weightInvMoments, nil, invariance.Within(weightInvMergeTol, 0), func(t *testing.T, resp *types.Response) {
		if resp.Components == nil || len(resp.Components.Aggregations) == 0 {
			t.Fatal("no aggregation components")
		}
		wzsAssertMomentKeys(t, resp.Components.Aggregations)
	}))

	// An archive crosstab never fans out: every cell runs the fused arm,
	// or the buffered one when fusion is off, whatever the worker knobs.
	crosstab := invariance.Group{Name: "archive", Ref: invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.File, DecodeWorkers: 1, ShardWorkers: 1}, Arm: observe.ArmFusedCrosstab}}
	for _, l := range []invariance.Layout{invariance.File, invariance.Archive} {
		for _, w := range []int{1, 2, 3, 8} {
			if l == invariance.File && w == 1 {
				continue
			}
			crosstab.Cells = append(crosstab.Cells, invariance.Cell{Knobs: invariance.Knobs{Layout: l, DecodeWorkers: w, ShardWorkers: w}, Arm: observe.ArmFusedCrosstab})
		}
		for _, w := range []int{1, 4} {
			crosstab.Cells = append(crosstab.Cells, invariance.Cell{Knobs: invariance.Knobs{Layout: l, DecodeWorkers: w, ShardWorkers: w, Mode: invariance.Buffered}, Arm: observe.ArmBuffered})
		}
	}
	xt := func() *types.Request { return wzsCrosstabRequest("") }
	if ok, reason := processing.CanFuseCrosstab(wzsCrosstabRequest("wz.pulse"), wzsSchema(), New(wz).Extensions()); !ok {
		t.Fatalf("CanFuseCrosstab rejected the weighted overlay crosstab: %s", reason)
	}
	invariance.Run(t, weightInvFamily("crosstab", wzCohorts, []invariance.Group{crosstab}, xt, weightInvFusion, nil, weightInvCrosstabCheck))
}

// TestWeightInvariance_BigFile: the weighted core aggregators on the
// per-segment reducer, DecodeWorkers 2/3/7/8 over a file above the
// parallel-decode threshold ≡ its serial read.
func TestWeightInvariance_BigFile(t *testing.T) {
	t.Parallel()

	cfg, osFs, dir := svcInvDisk(t)
	path := filepath.Join(dir, "weighted.pulse")
	recs, nullAt := weightModesRows(parallelDecodeRecordThreshold+4096, 0)
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, weightModesSchema(), recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	cohorts := map[invariance.Layout]svcInvCohort{invariance.BigFile: {cfg, path}}
	invariance.Run(t, weightInvFamily("core", cohorts, []invariance.Group{invariance.DecodeGroup("decode", invariance.BigFile)}, weightInvCore, nil, invariance.Within(weightInvMergeTol, 0), assertWeightedUseful))
}
