package pulse_test

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// grouped_components_invariance_test.go is the grouped-components family
// on the shared worker-invariance harness: per-group aggregator
// Components (components.aggregations[*].groups) are keyed to the
// REQUEST, never to the execution arm. For a plain and a fan-out
// grouper over a built-in slot on a nullable field, a floor-only one, a
// probability-weighted one and a Streamable + Mergeable extension
// aggregator, Data plus Components.Aggregations (Run differs by design:
// shard_count) are the same bits under
//
//   - archive (fast tier): the streaming single file ≡ the forced
//     buffered arm (ATTR_PERCENTILE: non-streamable, no output column) ≡
//     a 3-shard archive serial and under ShardWorkers 2/3/8 and
//     DecodeWorkers 7;
//   - decode (heavy tier, grouped_components_invariance_heavy_test.go):
//     DecodeWorkers 2/3/7/8 over a file above the parallel-decode
//     threshold ≡ serial.
//
// Every parallel cell must take its parallel arm and MERGE the
// extension aggregator's partials (its probe counts Merge calls), so a
// silent serial fallback cannot pass.

// gaInvAnswer is the arm-invariant document.
type gaInvAnswer struct {
	Data []map[string]any
	Aggs []types.AggregationComponents
}

// newGAInvFixture writes the twin and its 3-shard archive (hermetic);
// heavy adds the decode-threshold file on disk.
func newGAInvFixture(t *testing.T, heavy bool) map[invariance.Layout]invCohort {
	t.Helper()
	mem := afero.NewMemMapFs()
	s := paritySchema(t)
	writeParityCohort(t, mem, "twin.pulse", s, 0, paritySmallRows)
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeParityCohort(t, mem, path, s, i*paritySmallRows/3, (i+1)*paritySmallRows/3)
	}
	w := gaOpen(t, pulse.Options{FS: mem}, &parityProbe{})
	if _, err := w.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	out := map[invariance.Layout]invCohort{
		invariance.File:    {mem, "twin.pulse"},
		invariance.Archive: {mem, "archive.pulse"},
	}
	if heavy {
		dir := t.TempDir()
		writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), s, 0, parityLargeRows)
		out[invariance.BigFile] = invCohort{afero.NewBasePathFs(afero.NewOsFs(), dir), "large.pulse"}
	}
	return out
}

// gaInvFamilies: one family per grouper over groups.
func gaInvFamilies(cohorts map[invariance.Layout]invCohort, groups []invariance.Group) []invariance.Family[gaInvAnswer] {
	var names []string
	for name := range gaGroupers() {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []invariance.Family[gaInvAnswer]
	for _, name := range names {
		grp := gaGroupers()[name]
		out = append(out, invariance.Family[gaInvAnswer]{
			Name:   name,
			Groups: groups,
			Run: func(t *testing.T, k invariance.Knobs) invariance.Result[gaInvAnswer] {
				co, ok := cohorts[k.Layout]
				if !ok {
					t.Fatalf("layout %s not built in this tier", k.Layout)
				}
				probe := &parityProbe{}
				p, arm := invEngine(t, pulse.Options{Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
					paritySumRegistration(probe, gaExtSum, true),
				}}}, co, k)
				req := &types.Request{Cohort: &types.Cohort{Filename: co.path}, Groups: []*types.Group{grp}, Aggregations: gaAggregations()}
				if k.Mode == invariance.Buffered {
					req.Attributes = []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"}}
				}
				resp := gcProcess(t, p, req)
				if resp.Components == nil {
					t.Fatalf("%s: no Components", k)
				}
				a := arm()
				if (a == observe.ArmShardParallel || a == observe.ArmParallelDecode) && probe.merges.Load() == 0 {
					t.Fatalf("%s: the extension aggregator was never merged on arm %s", k, a)
				}
				return invariance.Result[gaInvAnswer]{Answer: gaInvAnswer{Data: resp.Data, Aggs: resp.Components.Aggregations}, Arm: a}
			},
			Check: func(t *testing.T, _ invariance.Knobs, ref gaInvAnswer) {
				gaAssertGroups(t, &types.Response{Data: ref.Data, Components: &types.ResponseComponents{Aggregations: ref.Aggs}})
			},
		})
	}
	return out
}

// TestGroupedComponentsInvariance_Archive is the fast-tier half of the
// grouped-components family: the archive group and the buffered arm
// against the streaming single file.
func TestGroupedComponentsInvariance_Archive(t *testing.T) {
	t.Parallel()

	archive := invariance.ArchiveGroup()
	archive.Ref.Arm = observe.ArmStreaming
	archive.Cells = append(archive.Cells, invariance.BufferedCell())
	for _, f := range gaInvFamilies(newGAInvFixture(t, false), []invariance.Group{archive}) {
		invariance.Run(t, f)
	}
}
