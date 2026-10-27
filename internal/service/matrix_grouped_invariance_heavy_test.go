//go:build heavy

package service

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/spf13/afero"
)

// TestMatrixGrouped_WorkerInvariant is the grouped worker-invariance
// gate (the per-bucket twin of TestMatrixCovariance_WorkerInvariant).
func TestMatrixGrouped_WorkerInvariant(t *testing.T) {
	t.Parallel()

	schema := groupedInvSchema(t)
	B := linalg.MergeBlockSize

	for _, g := range groupedInvGroupers {
		for _, v := range groupedInvVariants {
			v.withVehicle = false
			if !processing.CanMergeRequest(groupedInvRequest("x.pulse", v, g), schema) {
				t.Fatalf("%s %s: the merge gate refuses a grouped matrix-only request", g.name, v)
			}
		}
	}

	t.Run("DecodeWorkers", func(t *testing.T) {
		dir := t.TempDir()
		osFs := afero.NewOsFs()
		cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
		if err != nil {
			t.Fatalf("fs.New: %v", err)
		}
		n := 25*B + 2777
		if n < parallelDecodeRecordThreshold {
			t.Fatalf("cohort of %d records is below the parallel-decode threshold", n)
		}
		recs, nullAt := groupedInvRows(n, 0)
		path := dir + "/grouped_inv.pulse"
		if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, g := range groupedInvGroupers {
			for _, v := range groupedInvVariants {
				t.Run(g.name+"_"+v.String(), func(t *testing.T) {
					req := groupedInvRequest(path, v, g)
					serial := runGroupedInv(t, cfg, req, g, 1, 0)
					if want := g.common + g.thin; serial.instances != want {
						t.Fatalf("serial fed %d vehicle instances, want %d", serial.instances, want)
					}
					for _, workers := range []int{2, 3, 7, 8} {
						got := runGroupedInv(t, cfg, req, g, workers, 0)
						if want := int64(workers)*g.common + g.thin; got.instances != want {
							t.Fatalf("DecodeWorkers=%d fed %d vehicle instances, want %d — the decode reducer did not engage", workers, got.instances, want)
						}
						assertGroupedInvEqual(t, fmt.Sprintf("DecodeWorkers=%d", workers), got, serial)
					}
				})
			}
		}
	})

	t.Run("ShardWorkers", func(t *testing.T) {
		shardRows := []int{3 * B, 5000, B, 123, 9000, 2*B + 1, 777, 6000}
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "archive.pulse", groupedInvArchive(t, schema, shardRows), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		for _, g := range groupedInvGroupers {
			for _, v := range groupedInvVariants {
				t.Run(g.name+"_"+v.String(), func(t *testing.T) {
					req := groupedInvRequest("archive.pulse", v, g)
					serial := runGroupedInv(t, cfg, req, g, 0, 1)
					for _, workers := range []int{2, 3, 8} {
						got := runGroupedInv(t, cfg, req, g, 0, workers)
						if want := int64(len(shardRows))*g.common + g.thin; got.instances != want {
							t.Fatalf("ShardWorkers=%d fed %d vehicle instances, want %d — the shard reducer did not engage", workers, got.instances, want)
						}
						assertGroupedInvEqual(t, fmt.Sprintf("ShardWorkers=%d", workers), got, serial)
					}
				})
			}
		}
	})
}
