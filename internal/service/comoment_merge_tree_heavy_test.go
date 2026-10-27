//go:build heavy

package service

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/linalg"
	"github.com/spf13/afero"
)

// TestCoMomentMergeTree_WorkerInvariant is the BlockMerger gate: the
// full CoMoment state (N, invalid-weight count, W, means and every
// pair's count, weight and M2) is BIT-identical across serial and every
// worker count, through the real parallel-decode and shard dispatch,
// listwise and pairwise, weighted and unweighted.
func TestCoMomentMergeTree_WorkerInvariant(t *testing.T) {
	schema := coMomentSchema()
	B := linalg.MergeBlockSize

	t.Run("DecodeWorkers", func(t *testing.T) {
		// Parallel decode needs mmap, so these cohorts live on a real
		// (temporary) directory; the shard arm below is hermetic.
		dir := t.TempDir()
		osFs := afero.NewOsFs()
		cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
		if err != nil {
			t.Fatalf("fs.New: %v", err)
		}
		for _, n := range []int{25 * B, 25*B + 2777} {
			if n < parallelDecodeRecordThreshold {
				t.Fatalf("cohort of %d records is below the parallel-decode threshold", n)
			}
			recs, nullAt := coMomentRows(n, 0)
			path := fmt.Sprintf("%s/comoment_%d.pulse", dir, n)
			if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			for _, v := range coMomentVariants {
				t.Run(fmt.Sprintf("n%d_%s_weighted_%v", n, v.mode, v.weighted), func(t *testing.T) {
					req := coMomentRequest(path, v.mode, v.weighted)
					serial := runCoMomentVehicle(t, cfg, req, 1, 0)
					if serial.instances != 1 {
						t.Fatalf("serial run fed %d instances, want 1", serial.instances)
					}
					assertWordsClose(t, serial.words, referenceCoMomentWords(t, n, v.mode, v.weighted))
					for _, workers := range []int{2, 3, 7, 8} {
						got := runCoMomentVehicle(t, cfg, req, workers, 0)
						if got.instances != int64(workers) {
							t.Fatalf("DecodeWorkers=%d fed %d instances — the parallel reducer did not engage", workers, got.instances)
						}
						if i := firstWordDiff(got.words, serial.words); i != -1 {
							t.Errorf("DecodeWorkers=%d differs from serial at word %d", workers, i)
						}
					}
				})
			}
		}
	})

	t.Run("ShardWorkers", func(t *testing.T) {
		shardRows := []int{3 * B, 5000, B, 123, 9000, 2*B + 1, 777, 6000}
		total := 0
		for _, n := range shardRows {
			total += n
		}
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "archive.pulse", coMomentArchive(t, schema, shardRows), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		// The single-file twin holds the same rows in the same order;
		// shard edges fall inside merge blocks.
		recs, nullAt := coMomentRows(total, 0)
		if err := afero.WriteFile(cfg.Fs(), "single.pulse", writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, v := range coMomentVariants {
			t.Run(fmt.Sprintf("%s_weighted_%v", v.mode, v.weighted), func(t *testing.T) {
				req := coMomentRequest("archive.pulse", v.mode, v.weighted)
				serial := runCoMomentVehicle(t, cfg, req, 0, 1)
				if serial.instances != 1 {
					t.Fatalf("serial run fed %d instances, want 1", serial.instances)
				}
				assertWordsClose(t, serial.words, referenceCoMomentWords(t, total, v.mode, v.weighted))
				for _, workers := range []int{2, 3, 8} {
					got := runCoMomentVehicle(t, cfg, req, 0, workers)
					if got.instances != int64(len(shardRows)) {
						t.Fatalf("ShardWorkers=%d fed %d instances — the shard reducer did not engage", workers, got.instances)
					}
					if i := firstWordDiff(got.words, serial.words); i != -1 {
						t.Errorf("ShardWorkers=%d differs from serial at word %d", workers, i)
					}
				}

				// Blocks key by the GLOBAL record ordinal, so the archive
				// and its single-file twin fold the same blocks in the
				// same tree: the same bits.
				single := runCoMomentVehicle(t, cfg, coMomentRequest("single.pulse", v.mode, v.weighted), 0, 0)
				if i := firstWordDiff(single.words, serial.words); i != -1 {
					t.Errorf("archive differs from its single-file twin at word %d", i)
				}
			})
		}
	})

	t.Run("OneShardArchiveMatchesSingleFile", func(t *testing.T) {
		// One shard is block-for-block its single-file twin: same bits.
		n := 3*B + 55
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "one.pulse", coMomentArchive(t, schema, []int{n}), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		recs, nullAt := coMomentRows(n, 0)
		if err := afero.WriteFile(cfg.Fs(), "twin.pulse", writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, v := range coMomentVariants {
			a := runCoMomentVehicle(t, cfg, coMomentRequest("one.pulse", v.mode, v.weighted), 0, 0)
			b := runCoMomentVehicle(t, cfg, coMomentRequest("twin.pulse", v.mode, v.weighted), 0, 0)
			if i := firstWordDiff(a.words, b.words); i != -1 {
				t.Errorf("%s weighted=%v: one-shard archive differs from its single file at word %d", v.mode, v.weighted, i)
			}
		}
	})
}
