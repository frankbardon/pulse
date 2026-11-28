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
// covariance) and buffered (an AGG_MEDIAN). The peak is the live heap
// (peakLiveAbove, exact under the stop-the-world collector
// rerunUnderSTWGC runs the test under): the estimate is an allocation
// upper bound with GC headroom, so the live peak lands well below it,
// and the band catches a dropped or restored term, not a few percent.
//
// E7-S3 audit: at 100,000 entries a shard the one canonical dictionary
// decode (11 MB) sat inside the estimate's fixed run base, so dropping
// the schema-decode term (limits.EstimateMemory's SchemaDecodes x
// SchemaBytes) passed the band; at 250,000 it fails it. The dictionary
// archive is still dropped before the matrix cases: a sampled peak
// counted garbage up to the GC goal the live fixture raised (18 -> 47
// MB); the live peak does not, but nothing reads it there.
func TestArchiveMemory_EstimatedBytesRatio(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate
	if rerunUnderSTWGC(t) {
		return
	}

	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "dict.pulse", 4, 4096, 250_000)
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
		dict bool // over the in-memory dictionary archive, else the OS one
		req  func() *types.Request
	}{
		{"dictionary archive", true, func() *types.Request { return dictArchiveRequest("dict.pulse", false) }},
		{"dictionary archive grouped", true, func() *types.Request { return dictArchiveRequest("dict.pulse", true) }},
		// A grouped total() (U74 E3-S1, #494): ShardWorkers 4 runs the
		// two-phase pre-pass, then the grouped reduce.
		{"dictionary archive grouped total", true, func() *types.Request { return totalArchiveRequest("dict.pulse", true, "x0 / total(x0)") }},
		{"matrix scale archive", false, func() *types.Request { return matrixScaleRequest(scale, p, "pairwise", false) }},
		// The buffered arm (an order statistic): every record is
		// materialised, so the payload term's headroom over the largest
		// shard alone must still bound the peak.
		{"matrix scale archive buffered", false, func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: scale}, Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x001", Label: "m"}}}
		}},
	}
	for _, c := range cases {
		cfg := osCfg
		if c.dict {
			cfg = memCfg
		} else {
			// The dictionary archive is garbage from here on (the
			// dictionary cases come first).
			mem, memCfg = nil, nil
		}
		for _, workers := range []int{1, 4} {
			t.Run(fmt.Sprintf("%s/shards=%d", c.name, workers), func(t *testing.T) {
				svc := New(cfg)
				svc.SetShardWorkers(workers)
				estimate := float64(archiveMemoryEstimate(t, svc, c.req()))
				peak, err := peakLiveAbove(func() error {
					_, err := svc.Process(context.Background(), c.req())
					return err
				})
				if err != nil {
					t.Fatalf("Process: %v", err)
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
