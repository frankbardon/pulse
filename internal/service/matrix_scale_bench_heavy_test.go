//go:build heavy

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/linalg"
)

// TestMatrixScale_EstimatedBytesRatio: predict's estimated_bytes tracks
// the live heap a matrix run really holds. Over ~100K rows (25 merge
// blocks) × 4 seg buckets at p = 50 the merge-block state dominates the
// run, so the measured peak (minimum of three sampled passes) over the
// predicted state must sit in a loose band — loose because the peak is
// sampled and carries decode buffers and Go headers the estimate leaves
// out. It catches an estimate that loses a whole factor (the block
// count, the buckets, a word count far off), not a few percent; the
// exact per-state bytes are pinned by TestAccumulatorBytes (vectors) and
// TestAccumulatorBytesMatchesCoMomentLayout (linalg).
func TestMatrixScale_EstimatedBytesRatio(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const p = 50
	rows := 25 * linalg.MergeBlockSize
	dir := t.TempDir()
	path := matrixScaleCohort(t, dir, rows, p, 0)
	cfg := matrixScaleOSConfig(t, dir)
	for _, mode := range []string{"listwise", "pairwise"} {
		t.Run(mode, func(t *testing.T) {
			predicted := float64(matrixScalePredictedBytes(t, matrixScaleRequest(path, p, mode, true)))
			svc := New(cfg)
			svc.SetDecodeWorkers(1)
			peak := -1.0
			for range 3 {
				got, err := measurePeakHeap(func() error {
					resp, err := svc.Process(context.Background(), matrixScaleRequest(path, p, mode, true))
					if err == nil && len(resp.Matrices) != 4 {
						return fmt.Errorf("%d matrices, want one per seg bucket (4)", len(resp.Matrices))
					}
					return err
				})
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if peak < 0 || got < peak {
					peak = got
				}
			}
			ratio := peak / predicted
			t.Logf("%s: measured peak %.2f MB, predicted state %.2f MB, ratio %.2f", mode, peak/(1<<20), predicted/(1<<20), ratio)
			if ratio < matrixScaleRatioMin[mode] || ratio > matrixScaleRatioMax[mode] {
				t.Errorf("%s: measured / predicted = %.2f, want within [%.2f, %.2f]", mode, ratio, matrixScaleRatioMin[mode], matrixScaleRatioMax[mode])
			}
		})
	}
}
