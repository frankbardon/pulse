//go:build heavy

package service

import (
	"context"
	"path/filepath"
	"testing"
)

// TestShardStream_FlattenMemoryBoundedByShard: flatten and the archive
// arm hold O(one shard), never O(archive). The same run over 4 and 32
// shards (8x the bytes) is measured: a pure flatten ALLOCATES fixed
// buffers only (records stream through one window); a filtered run's
// per-row decode garbage is collected as it goes, so its PEAK heap must
// not grow with the archive — a whole-output buffer (the 32-shard
// output is ~15 MB) would.
func TestShardStream_FlattenMemoryBoundedByShard(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const recs = 40_000 // 12-byte records: ~480 KB per shard
	shardBytes := uint64(recs * 12)
	fixed := uint64(2 << 20)
	runs := []struct {
		name string
		opts FilterToFileOptions
	}{
		{"pure_flatten", FilterToFileOptions{Flatten: true}},
		{"filtered_flatten", FilterToFileOptions{Flatten: true, Expression: "score >= 0.0"}},
		{"archive_arm", FilterToFileOptions{Expression: "score >= 0.0"}},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			var got [2]uint64
			for i, shards := range []int{4, 32} {
				svc, fsys, dir := streamOsEnv(t)
				archive := streamBuildArchive(t, svc, fsys, dir, shards, recs)
				dst := filepath.Join(dir, "out.pulse")
				runOn := func(svc *Service) error {
					_, err := svc.FilterToFileWithOptions(context.Background(), archive, dst, r.opts)
					return err
				}
				if r.opts.Expression == "" {
					got[i] = allocDuring(t, func() error { return runOn(svc) })
				} else {
					var points int
					got[i], points = liveHeapPeakDuring(t, fsys, runOn)
					if points < 2 {
						t.Fatalf("%d probe points: the probe is not on the filter's I/O path", points)
					}
				}
				assertNoStages(t, fsys, dir)
			}
			t.Logf("%s: 4 shards %d B, 32 shards %d B", r.name, got[0], got[1])
			if r.opts.Expression == "" {
				for i, g := range got {
					if g > fixed {
						t.Errorf("pure flatten #%d allocated %d B, want <= %d (fixed buffers, no shard held)", i, g, fixed)
					}
				}
			}
			if got[1] > got[0]+4*shardBytes {
				t.Errorf("memory grows with the archive: %d B at 4 shards, %d B at 32", got[0], got[1])
			}
		})
	}
}
