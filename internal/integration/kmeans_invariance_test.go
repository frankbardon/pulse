package integration_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// kmeans_invariance_test.go is the GROUP_KMEANS family on the shared
// worker-invariance harness. Labels are a function of the member tuples
// alone: the grouper is non-mergeable, so every arm runs it serially
// over the whole cohort, and the canonical point set makes row order
// irrelevant — the same rows answer the same whole response
// (LayoutFree, bit-exact; the sums are of integers / exact halves) on a
// single file, a shard archive in either shard order, under every
// ShardWorkers count (TestKMeansInvariance_Archive) and every
// DecodeWorkers count above the parallel-decode threshold
// (TestKMeansInvariance_Heavy). Each cell names the serial arm, so a
// fan-out the merge gate should refuse fails rather than passes.

// kmReq is the GROUP_KMEANS request (k 4, seed 17) over score / qty.
func kmReq(cohort string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Groups: []*types.Group{{Type: types.GROUP_KMEANS, Fields: []string{"score", "qty"},
			Params: json.RawMessage(`{"k":4,"seed":17}`)}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_SUM, Field: "qty", Label: "q"},
		},
	}
}

// kmInvReversed is the shard archive with its shards in reverse-rotated
// order (s2, s0, s1).
const kmInvReversed invariance.Layout = "archive_reversed"

// kmInvArm is the arm a non-mergeable grouper takes on every layout.
const kmInvArm = observe.ArmBuffered

// kmInvFamily is kmReq over cohorts.
func kmInvFamily(cohorts map[invariance.Layout]invCohort, groups []invariance.Group) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:   "kmeans",
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			c, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			p, arm := invEngine(t, pulse.Options{}, c, k)
			resp, err := p.Process(context.Background(), kmReq(c.path))
			if err != nil {
				t.Fatalf("%s: Process: %v", k, err)
			}
			return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: arm()}
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
			if resp.Components == nil || len(resp.Components.Groupers) != 1 || len(resp.Data) != 4 || resp.Components.Groupers[0].NNull == 0 {
				t.Fatalf("want 4 clusters, grouper components and null-score rows in n_null; got %d rows", len(resp.Data))
			}
		},
	}
}

// TestKMeansInvariance_Archive: the single file under DecodeWorkers 4,
// the shard archive and its reverse-rotated twin under every
// ShardWorkers count ≡ the single file read serially.
func TestKMeansInvariance_Archive(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	s := paritySchema(t)
	writeParityCohort(t, fsys, "twin.pulse", s, 0, paritySmallRows)
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeParityCohort(t, fsys, path, s, i*paritySmallRows/3, (i+1)*paritySmallRows/3)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.CreateShardArchive(ctx, "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(ctx, "reversed.pulse", []string{"s2.pulse", "s0.pulse", "s1.pulse"}); err != nil {
		t.Fatal(err)
	}
	cohorts := map[invariance.Layout]invCohort{
		invariance.File:    {fsys, "twin.pulse"},
		invariance.Archive: {fsys, "archive.pulse"},
		kmInvReversed:      {fsys, "reversed.pulse"},
	}
	g := invariance.ArchiveGroup()
	g.Ref.Arm = kmInvArm
	for _, c := range append([]invariance.Cell(nil), g.Cells...) {
		c.Layout = kmInvReversed
		g.Cells = append(g.Cells, c)
	}
	g.Cells = append(g.Cells, invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.File, DecodeWorkers: 4, ShardWorkers: 3}})
	for i := range g.Cells {
		g.Cells[i].Arm = kmInvArm
	}
	invariance.Run(t, kmInvFamily(cohorts, []invariance.Group{g}))
}
