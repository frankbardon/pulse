package service

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_serial_invariance_test.go is the matrix family's serial-fallback
// half on the shared worker-invariance harness: a matrix the merge gate
// refuses never fans out, so every cell — parallel knobs included — must
// run on the serial buffered arm and answer the serial whole response
// (LayoutFree), bitwise:
//
//   - rank: MAT_CORRELATION spearman / kendall × listwise / pairwise in
//     one request (a rank method is not mergeable) over a shard archive
//     (a 1-row and an empty shard) under ShardWorkers 1/2/3/8 and
//     DecodeWorkers 7 ≡ its single-file twin; heavy
//     (matrix_serial_invariance_heavy_test.go): DecodeWorkers 2/3/7/8
//     over a file above the parallel-decode threshold ≡ serial;
//   - sem_ordinal: the buffered WLSMV MAT_SEM over the brand ratings
//     (rows in record order) over an archive of the same rows ≡ the twin
//     under the same knobs.

// rankInvShardRows / rankInvBigShardRows: the fast and the heavy
// tier's archive splits.
var (
	rankInvShardRows    = []int{700, 1, 0, 1299}
	rankInvBigShardRows = []int{40_000, 1_234, 0, 60_000}
)

// newRankInvFixture writes the twin and the archive (hermetic); heavy
// writes both on disk at 101,234 rows, the twin above the
// parallel-decode threshold (so it is the BigFile layout too).
func newRankInvFixture(t *testing.T, heavy bool) map[invariance.Layout]svcInvCohort {
	t.Helper()
	schema := coMomentSchema()
	shards := rankInvShardRows
	if heavy {
		shards = rankInvBigShardRows
	}
	n := 0
	for _, r := range shards {
		n += r
	}
	recs, nullAt := coMomentRows(n, 0)
	cfg, wfs, dir := fs.NewMemMap(), afero.Fs(nil), ""
	if heavy {
		cfg, wfs, dir = svcInvDisk(t)
		dir += "/"
	} else {
		wfs = cfg.Fs()
	}
	file, archive := svcInvCohort{cfg, dir + "twin.pulse"}, svcInvCohort{cfg, dir + "archive.pulse"}
	if err := afero.WriteFile(wfs, file.path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(wfs, archive.path, svcInvArchive(t, schema, recs, nullAt, shards), 0o644); err != nil {
		t.Fatal(err)
	}
	out := map[invariance.Layout]svcInvCohort{invariance.File: file, invariance.Archive: archive}
	if heavy {
		out[invariance.BigFile] = file
	}
	return out
}

// rankInvRequest carries every rank method × missing-data rule.
func rankInvRequest() *types.Request {
	req := &types.Request{}
	for _, method := range []string{"spearman", "kendall"} {
		for _, missing := range []string{"listwise", "pairwise"} {
			req.Matrices = append(req.Matrices, types.MatrixSpec{Name: method + "_" + missing, Type: types.MAT_CORRELATION, Fields: []string{"x1", "x2", "x3"},
				Params: json.RawMessage(`{"method": "` + method + `", "missing": "` + missing + `"}`)})
		}
	}
	return req
}

// rankInvFamily is the rank request over groups.
func rankInvFamily(t *testing.T, cohorts map[invariance.Layout]svcInvCohort, groups []invariance.Group) invariance.Family[*types.Response] {
	if processing.CanMergeRequest(rankInvRequest(), coMomentSchema()) {
		t.Fatal("the merge gate admits a rank-method request")
	}
	return invariance.Family[*types.Response]{
		Name:   "rank",
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			c, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			return svcInvProcess(t, c, k, rankInvRequest(), nil)
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
			if len(resp.Matrices) != 4 {
				t.Fatalf("%d matrices, want 4", len(resp.Matrices))
			}
			for _, m := range resp.Matrices {
				if m.Error != nil || len(m.Primary.Values) != 3 || m.Primary.Values[0][1] == 0 {
					t.Fatalf("%s: degenerate result %+v", m.Name, m)
				}
			}
		},
	}
}

// semOrdinalInvShardRows splits the 1,200 brand rows (a 1-row and an
// empty shard).
var semOrdinalInvShardRows = []int{1, 550, 0, 649}

// TestMatrixSerialInvariance_Archive: the rank and ordinal-SEM families
// over their archive groups, every cell on the serial buffered arm.
func TestMatrixSerialInvariance_Archive(t *testing.T) {
	t.Parallel()

	invariance.Run(t, rankInvFamily(t, newRankInvFixture(t, false), []invariance.Group{svcInvSerialArchiveGroup(observe.ArmBuffered)}))

	schema, rows, cols := semRows(t, semTestdata+"brand_data.csv", nil, nil)
	none := func(int, int) bool { return false }
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "twin.pulse", writeNullablePulse(t, schema, rows, none), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(cfg.Fs(), "archive.pulse", svcInvArchive(t, schema, rows, none, semOrdinalInvShardRows), 0o644); err != nil {
		t.Fatal(err)
	}
	cohorts := map[invariance.Layout]svcInvCohort{invariance.File: {cfg, "twin.pulse"}, invariance.Archive: {cfg, "archive.pulse"}}
	spec := ordinalSpec(t, cols, "brand_ms", map[string]any{"estimator": "wlsmv", "ordered": []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3"}})
	invariance.Run(t, invariance.Family[*types.Response]{
		Name:   "sem_ordinal",
		Groups: []invariance.Group{svcInvSerialArchiveGroup(observe.ArmBuffered)},
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			return svcInvProcess(t, cohorts[k.Layout], k, &types.Request{Matrices: []types.MatrixSpec{spec}}, nil)
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
			if len(resp.Matrices) != 1 || resp.Matrices[0].Error != nil || resp.Matrices[0].Scalars["n"] != 1200 || len(resp.Matrices[0].Auxiliary["estimates"].Values) == 0 {
				t.Fatalf("degenerate ordinal fit: %+v", resp.Matrices)
			}
		},
	})
}
