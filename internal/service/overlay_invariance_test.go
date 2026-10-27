package service

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// overlay_invariance_test.go is the overlay / post-hook family on the
// shared worker-invariance harness, over the multiplicity fixture
// (multSchema / multRows): a request carrying a request-level Holm
// block answers the same whole response (LayoutFree), bitwise,
//
//   - series: a grouped host with SERIES overlays (index vs total,
//     delta vs prior) folded in the shared grouped tail, test-free — so
//     the parallel arms take it and the block is the identity (the
//     reference equals the block-free run, assertNoTestArmIdentical);
//   - tests: one-sample t-tests aimed 2.6 and 2.0 standard errors off
//     the layout's mean (p ≈ 0.01 / 0.05 at any n, so the correction
//     method shows in p_adjusted), a split t, a Pearson r
//     and a Fisher exact — the parallel arms exclude tests, so every
//     cell runs serially and the post-hook corrects it (the reference's
//     p_adjusted is Holm over its raw p, assertProcessFold);
//
// over a shard archive (a 1-row and an empty shard) under ShardWorkers
// 1/2/3/8 and DecodeWorkers 7 ≡ its single-file twin, and (heavy,
// overlay_invariance_heavy_test.go) DecodeWorkers 2/3/7/8 over a file
// above the parallel-decode threshold ≡ serial.

// overlayInvShardRows split the twin's 2,000 rows.
var overlayInvShardRows = []int{1, 900, 0, 1099}

// newOverlayInvFixture writes the twin and the archive (hermetic); heavy
// adds the decode-threshold file on disk. rows maps each layout to its
// record count (the tests shape aims at that many rows' mean).
func newOverlayInvFixture(t *testing.T, heavy bool) (map[invariance.Layout]svcInvCohort, map[invariance.Layout]int) {
	t.Helper()
	n := 0
	for _, r := range overlayInvShardRows {
		n += r
	}
	mem := fs.NewMemMap()
	recs := multRows(n, 0)
	if err := afero.WriteFile(mem.Fs(), "twin.pulse", writeNullablePulse(t, multSchema(), recs, nil), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(mem.Fs(), "archive.pulse", svcInvArchive(t, multSchema(), recs, func(int, int) bool { return false }, overlayInvShardRows), 0o644); err != nil {
		t.Fatal(err)
	}
	cohorts := map[invariance.Layout]svcInvCohort{invariance.File: {mem, "twin.pulse"}, invariance.Archive: {mem, "archive.pulse"}}
	rows := map[invariance.Layout]int{invariance.File: n, invariance.Archive: n}
	if heavy {
		big := parallelDecodeRecordThreshold + 4096
		cfg, osFs, dir := svcInvDisk(t)
		path := filepath.Join(dir, "big.pulse")
		if err := afero.WriteFile(osFs, path, writeNullablePulse(t, multSchema(), multRows(big, 0), nil), 0o644); err != nil {
			t.Fatal(err)
		}
		cohorts[invariance.BigFile], rows[invariance.BigFile] = svcInvCohort{cfg, path}, big
	}
	return cohorts, rows
}

// overlayInvMuOffsets are one-sample t-test means c·sd/√n off multRows'
// x mean over n rows, c = 2.6 and 2.0: p ≈ 0.01 and 0.05 at any n, so
// Holm and every other method move them differently.
func overlayInvMuOffsets(n int) (float64, float64) {
	var s, ss float64
	for k := 0; k < n; k++ {
		x := 5 + 2*math.Sin(float64(k)) + 0.6*float64(k%2)
		s += x
		ss += x * x
	}
	mean := s / float64(n)
	se := math.Sqrt((ss/float64(n)-mean*mean)*float64(n)/float64(n-1)) / math.Sqrt(float64(n))
	return mean + 2.6*se, mean + 2.0*se
}

// overlayInvShape is one request of the family, block-free.
type overlayInvShape struct {
	name  string
	tests bool
	build func(n int) *types.Request
}

func overlayInvShapes() []overlayInvShape {
	return []overlayInvShape{
		{"series", false, func(int) *types.Request {
			return &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}, {Type: types.AGG_COUNT, Field: "x", Label: "n"}},
				Overlays: []types.OverlaySpec{
					{Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeGroup},
					{Kind: types.OverlayKindDeltaVsPrior, Scope: types.OverlayScopeGroup},
				},
			}
		}},
		{"tests", true, func(n int) *types.Request {
			muA, muB := overlayInvMuOffsets(n)
			return &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
				Tests: []*types.Test{
					{Type: types.TEST_T, Field: "x", Params: json.RawMessage(fmt.Sprintf(`{"mu":%v}`, muA)), Label: "a"},
					{Type: types.TEST_T, Field: "x", Params: json.RawMessage(fmt.Sprintf(`{"mu":%v}`, muB)), Label: "b"},
					{Type: types.TEST_T, Field: "x", SplitBy: "g", Label: "two"},
					{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Label: "r"},
					{Type: types.TEST_FISHER_EXACT, Rows: "g", Cols: "h", Label: "fisher"},
				},
			}
		}},
	}
}

// overlayInvGroups: the tests shape runs every cell on the serial
// buffered arm.
func overlayInvGroups(s overlayInvShape, heavy bool) []invariance.Group {
	if s.tests {
		gs := []invariance.Group{svcInvSerialArchiveGroup(observe.ArmBuffered)}
		if heavy {
			gs = append(gs, svcInvSerialDecodeGroup("decode", invariance.BigFile, observe.ArmBuffered))
		}
		return gs
	}
	gs := []invariance.Group{invariance.ArchiveGroup()}
	if heavy {
		gs = append(gs, invariance.DecodeGroup("decode", invariance.BigFile))
	}
	return gs
}

// overlayInvFamily is shape s, under a Holm block, over groups.
func overlayInvFamily(cohorts map[invariance.Layout]svcInvCohort, rows map[invariance.Layout]int, s overlayInvShape, groups []invariance.Group) invariance.Family[*types.Response] {
	blocked := func(k invariance.Knobs) *types.Request {
		r := s.build(rows[k.Layout])
		r.Cohort = &types.Cohort{Filename: cohorts[k.Layout].path}
		r.Multiplicity = &types.Multiplicity{Method: types.MultiplicityMethodHolm}
		return r
	}
	return invariance.Family[*types.Response]{
		Name:   s.name,
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			c, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			return svcInvProcess(t, c, k, blocked(k), nil)
		},
		Check: func(t *testing.T, k invariance.Knobs, resp *types.Response) {
			c := cohorts[k.Layout]
			svc := New(c.cfg)
			svc.SetDecodeWorkers(k.DecodeWorkers)
			svc.SetShardWorkers(k.ShardWorkers)
			mk := func() *types.Request {
				r := s.build(rows[k.Layout])
				r.Cohort = &types.Cohort{Filename: c.path}
				return r
			}
			if s.tests {
				if len(resp.Tests) != 5 {
					t.Fatalf("%d test results, want 5", len(resp.Tests))
				}
				assertProcessFold(t, svc, mk)
				return
			}
			if len(resp.Data) != 2 || len(resp.Overlays) != 2 {
				t.Fatalf("degenerate run: %d rows, %d overlay layers", len(resp.Data), len(resp.Overlays))
			}
			assertNoTestArmIdentical(t, svc, mk)
		},
	}
}

// TestOverlayInvariance_Archive is the fast-tier half of the overlay /
// post-hook family; TestOverlayInvariance_Heavy adds the decode group.
func TestOverlayInvariance_Archive(t *testing.T) {
	t.Parallel()

	cohorts, rows := newOverlayInvFixture(t, false)
	for _, s := range overlayInvShapes() {
		invariance.Run(t, overlayInvFamily(cohorts, rows, s, overlayInvGroups(s, false)))
	}
}
