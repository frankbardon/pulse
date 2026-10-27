package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/types"
)

// wide_set_invariance_test.go is the wide-set family on the shared
// worker-invariance harness: a set_u256 column (206-entry dictionary,
// one bit per 64-bit word plus a tail bit per row, so a decode or a
// reducer that drops a word changes the answer) under every set
// aggregator — each with a MergeOnline, so the request takes either
// parallel arm — answers the same whole response (LayoutFree):
//
//   - archive: the rows split unevenly (a 1-row and an empty shard)
//     under ShardWorkers 1/2/3/8 and DecodeWorkers 7 ≡ the single-file
//     twin (TestWideSetInvariance_Archive);
//   - decode (heavy, wide_set_invariance_heavy_test.go): DecodeWorkers
//     2/3/7/8 over an on-disk file above the parallel-decode threshold ≡
//     serial.
//
// Every parallel cell must report its parallel arm (the harness), so a
// silent bail to the serial path fails rather than comparing serial
// against serial.

// wideSetInvBounds are the archive's shard edges (1, 400, 0, 499 and
// 300 rows).
var wideSetInvBounds = []int{0, 1, 401, 401, 900, 1200}

// writeWideSetRows writes rows [from, to) of the wide-set cohort at path.
func writeWideSetRows(t *testing.T, cfg *fs.Config, path string, schema *encoding.Schema, from, to int) {
	t.Helper()
	var payload bytes.Buffer
	for r := from; r < to; r++ {
		if err := encoding.WriteSetMask(&payload, encoding.FieldTypeSetU256, wideSetParityMask(r)); err != nil {
			t.Fatalf("write tags(%d): %v", r, err)
		}
		w := float64((r*37)%509) + 0.125
		if err := encoding.WriteFieldValue(&payload, encoding.FieldTypeF64, math.Float64bits(w)); err != nil {
			t.Fatalf("write weight(%d): %v", r, err)
		}
	}
	writeWideSetCohort(t, cfg.Fs(), path, schema, payload.Bytes())
}

// newWideSetInvFixture writes the twin and the archive (hermetic); heavy
// adds the decode-threshold file on disk.
func newWideSetInvFixture(t *testing.T, heavy bool) map[invariance.Layout]svcInvCohort {
	t.Helper()
	schema, _ := wideSetParityCohort(t, 0)
	mem := fs.NewMemMap()
	total := wideSetInvBounds[len(wideSetInvBounds)-1]
	writeWideSetRows(t, mem, "twin.pulse", schema, 0, total)
	var shards []string
	for i := 0; i+1 < len(wideSetInvBounds); i++ {
		p := fmt.Sprintf("shard_%d.pulse", i)
		writeWideSetRows(t, mem, p, schema, wideSetInvBounds[i], wideSetInvBounds[i+1])
		shards = append(shards, p)
	}
	if _, err := New(mem).CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	out := map[invariance.Layout]svcInvCohort{
		invariance.File:    {mem, "twin.pulse"},
		invariance.Archive: {mem, "archive.pulse"},
	}
	if heavy {
		cfg, _, dir := svcInvDisk(t)
		path := filepath.Join(dir, "wide_set_big.pulse")
		writeWideSetRows(t, cfg, path, schema, 0, parallelDecodeRecordThreshold+4096)
		out[invariance.BigFile] = svcInvCohort{cfg, path}
	}
	return out
}

// wideSetInvFamily is the set-aggregation request over groups.
func wideSetInvFamily(cohorts map[invariance.Layout]svcInvCohort, groups []invariance.Group) invariance.Family[*types.Response] {
	return invariance.Family[*types.Response]{
		Name:   "wide_set",
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			c, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			return svcInvProcess(t, c, k, wideSetParityRequest(c.path), nil)
		},
		Check: func(t *testing.T, k invariance.Knobs, resp *types.Response) {
			if len(resp.Data) != 1 {
				t.Fatalf("want one aggregation row, got %d", len(resp.Data))
			}
			assertWideSetRowNonDegenerate(t, resp.Data[0])
			if resp.Components == nil || resp.Components.Run == nil || resp.Components.Run.TotalRecords == 0 {
				t.Fatalf("no Run components: %+v", resp.Components)
			}
		},
	}
}

// TestWideSetInvariance_Archive is the fast-tier half of the wide-set
// family; TestWideSetInvariance_Heavy adds the parallel-decode group.
func TestWideSetInvariance_Archive(t *testing.T) {
	t.Parallel()

	invariance.Run(t, wideSetInvFamily(newWideSetInvFixture(t, false), []invariance.Group{invariance.ArchiveGroup()}))
}
