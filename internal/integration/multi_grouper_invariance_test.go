package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// multi_grouper_invariance_test.go is the multi-grouper family on the
// shared worker-invariance harness: every mergeable #422 case (two or
// three groupers, fan-out, nulls, an include order) answers the SAME
// BITS — the whole response (LayoutFree): rows, Components per slot,
// per group and per grouper, the grouped matrices, warnings — weighted
// and unweighted slots in one request, under
//
//   - archive: the bit cohort split unevenly (two 1-row shards, an empty
//     shard, edges inside merge blocks) under ShardWorkers 1/2/3/8 and
//     DecodeWorkers 7 ≡ its single-file twin — three cases on a 4,873-row
//     split in the fast tier, every case on 13,065 rows in the heavy one;
//   - decode (heavy tier, multi_grouper_invariance_heavy_test.go):
//     DecodeWorkers 2/3/7/8 over a 103,634-record file ≡ serial.
//
// Every parallel cell must take its parallel arm (the harness), so no
// knob compares serial against serial. The slots are a count and the
// merge-block ones (vector sum / mean, covariance): they merge through
// processing.BlockMerger's fixed tree, and the floor's weighted keys
// (sum_weights, n_eff) are binned reproducible sums, so bitwise is the
// rule; AGG_STDDEV's Chan-Welford merge is ULP-bounded by design and is
// not here (.claude/reference/execution-modes/shards-and-merge.md,
// Blocked merge).

// mgInvBounds are the archive's shard edges — two 1-row shards, an
// empty shard, edges inside merge blocks — over the heavy tier's
// 13,065-record twin; mgInvSmokeBounds the fast tier's (4,873 records,
// the same edge kinds).
var (
	mgInvBounds      = []int{0, 1, 2, 2, 4097, 4098, 9000, 3*4096 + 777}
	mgInvSmokeBounds = []int{0, 1, 2, 2, 4097, 4098, 4096 + 777}
)

// mgInvSmokeCases run in the fast tier: two groupers, a fan-out, three
// groupers with an outer null and a middle fan-out.
var mgInvSmokeCases = map[string]bool{"CategoryXCategory": true, "SetPerElementFanOutXCategory": true, "ThreeWithNullAndFanOut": true}

// newMGInvFixture writes the twin and the archive (hermetic; the smoke
// split in the fast tier); heavy adds the decode-threshold file on disk.
func newMGInvFixture(t *testing.T, heavy bool) map[invariance.Layout]invCohort {
	t.Helper()
	writer := func(fsys afero.Fs) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: fsys})
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		return p
	}
	bounds := mgInvSmokeBounds
	if heavy {
		bounds = mgInvBounds
	}
	mem := afero.NewMemMapFs()
	w := writer(mem)
	mgWriteBitsCohort(t, w, "small.pulse", 0, bounds[len(bounds)-1])
	var shards []string
	for i := 0; i+1 < len(bounds); i++ {
		path := fmt.Sprintf("bits_s%d.pulse", i)
		mgWriteBitsCohort(t, w, path, bounds[i], bounds[i+1])
		shards = append(shards, path)
	}
	if _, err := w.CreateShardArchive(context.Background(), "bits_archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	out := map[invariance.Layout]invCohort{
		invariance.File:    {mem, "small.pulse"},
		invariance.Archive: {mem, "bits_archive.pulse"},
	}
	if heavy {
		disk := afero.NewBasePathFs(afero.NewOsFs(), t.TempDir())
		mgWriteBitsCohort(t, writer(disk), "big.pulse", 0, mgDecodeFiller+1234)
		out[invariance.BigFile] = invCohort{disk, "big.pulse"}
	}
	return out
}

// mgInvRequest is groups over a count and the merge-block slots, each
// float slot unweighted and weighted by score.
func mgInvRequest(cohort string, groups []*types.Group) *types.Request {
	w := types.SlotWeightField("score")
	return &types.Request{
		Cohort: &types.Cohort{Filename: cohort},
		Groups: groups,
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
			{Type: types.AGG_VEC_SUM, Fields: []string{"score", "y"}, Label: "s"},
			{Type: types.AGG_VEC_MEAN, Fields: []string{"score", "y"}, Label: "m"},
			{Type: types.AGG_VEC_SUM, Fields: []string{"score", "y"}, Label: "ws", Weight: w},
			{Type: types.AGG_VEC_MEAN, Fields: []string{"score", "y"}, Label: "wm", Weight: w},
		},
		Matrices: []types.MatrixSpec{
			{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"score", "y"}},
			{Type: types.MAT_COVARIANCE, Name: "wcov", Fields: []string{"score", "y"}, Weight: w},
		},
	}
}

// mgInvFamilies: one family per mergeable case keep admits, over groups.
func mgInvFamilies(cohorts map[invariance.Layout]invCohort, keep func(c mgCase) bool, groups []invariance.Group) []invariance.Family[*types.Response] {
	var out []invariance.Family[*types.Response]
	for _, c := range mgCases() {
		if !mgMergeable(&types.Request{Groups: c.groups}) || !keep(c) {
			continue
		}
		out = append(out, invariance.Family[*types.Response]{
			Name:   c.name,
			Groups: groups,
			Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
				co, ok := cohorts[k.Layout]
				if !ok {
					t.Fatalf("layout %s not built in this tier", k.Layout)
				}
				p, arm := invEngine(t, pulse.Options{}, co, k)
				resp, err := p.Process(context.Background(), mgInvRequest(co.path, c.groups))
				if err != nil {
					t.Fatalf("%s: Process: %v", k, err)
				}
				return invariance.Result[*types.Response]{Answer: invariance.LayoutFree(resp), Arm: arm()}
			},
			Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
				comps := resp.Components
				if len(resp.Data) == 0 || comps == nil || len(comps.Groupers) != len(c.groups) || len(resp.Matrices) != 2*len(resp.Data) {
					t.Fatalf("degenerate run: %d rows, %d matrices", len(resp.Data), len(resp.Matrices))
				}
				for _, a := range comps.Aggregations {
					if weighted := a.Label == "ws" || a.Label == "wm"; weighted != (a.SumWeights != nil) || len(a.Groups) != len(resp.Data) {
						t.Fatalf("slot %s: sum_weights present = %v, %d groups for %d rows", a.Label, a.SumWeights != nil, len(a.Groups), len(resp.Data))
					}
				}
			},
		})
	}
	return out
}

// TestMultiGrouperInvariance_Archive is the fast-tier smoke of the
// multi-grouper family: mgInvSmokeCases over the smoke archive.
// TestMultiGrouperInvariance_Heavy runs every case on the full archive.
func TestMultiGrouperInvariance_Archive(t *testing.T) {
	t.Parallel()

	keep := func(c mgCase) bool { return mgInvSmokeCases[c.name] }
	for _, f := range mgInvFamilies(newMGInvFixture(t, false), keep, []invariance.Group{invariance.ArchiveGroup()}) {
		invariance.Run(t, f)
	}
}
