//go:build heavy

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestArchiveMemory_EstimatedBytesRatio (E8-S3): over a shard archive
// MaxEstimatedMemory's estimate bounds the peak heap a run really
// reaches — with ShardWorkers 1 (the serial archive iterator) and 4
// (the per-shard parallel reducer). Two archives: one whose shards each
// carry a 100,000-entry dictionary, so its bytes and schema decodes
// dwarf records x stride (without the archive terms the estimate was
// ~1/5 of its peak), and the matrix scale archive (~100K rows x p = 50, 8
// shards), where the payload dominates — streamed (a pairwise
// covariance) and buffered (an AGG_MEDIAN). The peak is the minimum of
// three sampled passes (measurePeakHeap); the band is loose — the
// estimate is an allocation upper bound, so GC timing moves only how
// far below it the peak lands — and catches a dropped term, not a few
// percent.
func TestArchiveMemory_EstimatedBytesRatio(t *testing.T) {
	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "dict.pulse", 4, 4096, 100_000)
	memCfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	const p = 50
	dir := t.TempDir()
	scale := matrixScaleCohort(t, dir, 25*linalg.MergeBlockSize, p, 8)
	osCfg := matrixScaleOSConfig(t, dir)
	cases := []struct {
		name string
		cfg  *fs.Config
		req  func() *types.Request
	}{
		{"dictionary archive", memCfg, func() *types.Request { return dictArchiveRequest("dict.pulse", false) }},
		{"dictionary archive grouped", memCfg, func() *types.Request { return dictArchiveRequest("dict.pulse", true) }},
		{"matrix scale archive", osCfg, func() *types.Request { return matrixScaleRequest(scale, p, "pairwise", false) }},
		// The buffered arm (an order statistic): every record is
		// materialised, so the payload term's headroom over the largest
		// shard alone must still bound the peak.
		{"matrix scale archive buffered", osCfg, func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: scale}, Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x001", Label: "m"}}}
		}},
	}
	for _, c := range cases {
		for _, workers := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s/shards=%d", c.name, workers), func(t *testing.T) {
				svc := New(c.cfg)
				svc.SetShardWorkers(workers)
				estimate := float64(archiveMemoryEstimate(t, svc, c.req()))
				peak := -1.0
				for range 3 {
					got, err := measurePeakHeap(func() error {
						_, err := svc.Process(context.Background(), c.req())
						return err
					})
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					if peak < 0 || got < peak {
						peak = got
					}
				}
				ratio := peak / estimate
				t.Logf("measured peak %.1f MB, estimate %.1f MB, ratio %.2f", peak/(1<<20), estimate/(1<<20), ratio)
				if ratio < archiveRatioMin || ratio > archiveRatioMax {
					t.Errorf("measured / estimated = %.2f, want within [%.2f, %.2f]", ratio, archiveRatioMin, archiveRatioMax)
				}
			})
		}
	}
}
