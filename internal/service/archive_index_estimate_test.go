package service

import (
	"context"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// archive_index_estimate_test.go pins the union distinct-key estimate
// (U73 #498): an archive index records one HyperLogLog sketch per shard
// in its manifest, BuildIndex / ListIndexes report the merged estimate
// beside the unchanged per-shard-sum DistinctKeys, shard add / remove
// upkeep keeps it current, a record without a sketch makes it unknown
// (nil, never 0), and a single-file index reports its exact count.

// estimateTolerance is three standard errors of the documented
// 1.04/sqrt(1024) ≈ 3.25% relative error.
const estimateTolerance = 3 * 0.0325

// writeIDShard writes a single-file cohort holding ids [lo, hi).
func writeIDShard(t *testing.T, fsys afero.Fs, name string, lo, hi uint64) {
	t.Helper()
	rows := make([]archiveLookupRow, 0, hi-lo)
	for id := lo; id < hi; id++ {
		rows = append(rows, archiveLookupRow{id: id, region: "north", score: float64(id)})
	}
	writeArchiveLookupCohort(t, fsys, name, archiveLookupSchema("north"), rows)
}

// setupEstimateArchive builds "arch.pulse" from three shards whose ids
// overlap pairwise (0..3000, 1500..4500, 3000..6000: union 6000, sum
// 9000) and indexes it on id.
func setupEstimateArchive(t *testing.T) (*Service, afero.Fs, *BuildIndexResult) {
	t.Helper()
	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	writeIDShard(t, fsys, "a.pulse", 0, 3000)
	writeIDShard(t, fsys, "b.pulse", 1500, 4500)
	writeIDShard(t, fsys, "c.pulse", 3000, 6000)
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse", "c.pulse"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.BuildIndex(ctx, "arch.pulse", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, fsys, res
}

func assertEstimateNear(t *testing.T, what string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: estimate unknown (nil), want ≈%d", what, want)
	}
	if rel := (float64(*got) - float64(want)) / float64(want); rel < -estimateTolerance || rel > estimateTolerance {
		t.Errorf("%s: estimate %d, true union %d (relative error %.4f)", what, *got, want, rel)
	}
}

func listOnlyIndex(t *testing.T, svc *Service, path string) IndexInfo {
	t.Helper()
	infos, err := svc.ListIndexes(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("ListIndexes: %d indexes, want 1", len(infos))
	}
	return infos[0]
}

func TestArchiveIndexEstimate_OverlappingShards(t *testing.T) {
	t.Parallel()
	svc, fsys, res := setupEstimateArchive(t)
	if res.DistinctKeys != 9000 {
		t.Errorf("BuildIndex DistinctKeys = %d, want the per-shard sum 9000", res.DistinctKeys)
	}
	if res.IndexedRecords != 9000 {
		t.Errorf("BuildIndex IndexedRecords = %d, want 9000", res.IndexedRecords)
	}
	assertEstimateNear(t, "BuildIndex", res.DistinctKeysEstimate, 6000)

	info := listOnlyIndex(t, svc, "arch.pulse")
	if info.DistinctKeys != 9000 {
		t.Errorf("ListIndexes DistinctKeys = %d, want 9000", info.DistinctKeys)
	}
	assertEstimateNear(t, "ListIndexes", info.DistinctKeysEstimate, 6000)
	if *info.DistinctKeysEstimate != *res.DistinctKeysEstimate {
		t.Errorf("ListIndexes estimate %d != BuildIndex estimate %d", *info.DistinctKeysEstimate, *res.DistinctKeysEstimate)
	}
	for name, sh := range archiveShardRecords(t, fsys, []string{"id"}) {
		if _, ok := encx.DecodeIndexHLL(sh.HLL); !ok {
			t.Errorf("shard %s: manifest record carries no valid sketch", name)
		}
	}
}

func TestArchiveIndexEstimate_AbsentSketchIsUnknown(t *testing.T) {
	t.Parallel()
	svc, fsys, _ := setupEstimateArchive(t)
	// An older binary round-tripping the manifest drops "hll".
	rewriteArchiveManifest(t, fsys, []string{"id"}, func(sh *encx.IndexManifestShard) {
		if sh.Name == "b.pulse" {
			sh.HLL = ""
		}
	})
	info := listOnlyIndex(t, svc, "arch.pulse")
	if info.DistinctKeysEstimate != nil {
		t.Errorf("estimate %d with a sketch missing, want unknown (nil)", *info.DistinctKeysEstimate)
	}
	if info.DistinctKeys != 9000 {
		t.Errorf("DistinctKeys = %d, want 9000 unchanged", info.DistinctKeys)
	}
}

func TestArchiveIndexEstimate_UpkeepOnAddAndRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, fsys, _ := setupEstimateArchive(t)
	writeIDShard(t, fsys, "d.pulse", 5000, 8000)
	if _, err := svc.AddShard(ctx, "arch.pulse", "d.pulse"); err != nil {
		t.Fatal(err)
	}
	info := listOnlyIndex(t, svc, "arch.pulse")
	if info.DistinctKeys != 12000 {
		t.Errorf("after add: DistinctKeys = %d, want 12000", info.DistinctKeys)
	}
	assertEstimateNear(t, "after add", info.DistinctKeysEstimate, 8000)

	if _, err := svc.RemoveShard(ctx, "arch.pulse", "a.pulse"); err != nil {
		t.Fatal(err)
	}
	info = listOnlyIndex(t, svc, "arch.pulse")
	if info.DistinctKeys != 9000 {
		t.Errorf("after remove: DistinctKeys = %d, want 9000", info.DistinctKeys)
	}
	// ids 1500..8000 remain.
	assertEstimateNear(t, "after remove", info.DistinctKeysEstimate, 6500)
}

func TestIndexEstimate_SingleFileIsExact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	writeIDShard(t, fsys, "one.pulse", 0, 500)
	res, err := svc.BuildIndex(ctx, "one.pulse", []string{"id"})
	if err != nil {
		t.Fatal(err)
	}
	if res.DistinctKeysEstimate == nil || *res.DistinctKeysEstimate != 500 || res.DistinctKeys != 500 {
		t.Fatalf("BuildIndex: estimate %v, DistinctKeys %d; want exact 500", res.DistinctKeysEstimate, res.DistinctKeys)
	}
	if info := listOnlyIndex(t, svc, "one.pulse"); info.DistinctKeysEstimate == nil || *info.DistinctKeysEstimate != 500 {
		t.Errorf("ListIndexes (manifest): estimate %v, want exact 500", info.DistinctKeysEstimate)
	}
	if err := fsys.Remove(encx.IndexManifestPath("one.pulse")); err != nil {
		t.Fatal(err)
	}
	if info := listOnlyIndex(t, svc, "one.pulse"); info.DistinctKeysEstimate == nil || *info.DistinctKeysEstimate != 500 {
		t.Errorf("ListIndexes (sidecar scan): estimate %v, want exact 500", info.DistinctKeysEstimate)
	}
}
