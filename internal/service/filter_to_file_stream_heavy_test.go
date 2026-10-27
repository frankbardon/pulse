//go:build heavy

package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// TestShardStream_FilterSingleFileMemoryBounded: the single-file and
// anchored arms hold the copy window, never the input or the output.
// The same run over 40k and 400k rows (10x the bytes) is measured: a
// predicate-free copy ALLOCATES fixed buffers only; a filtered run's
// per-row decode garbage is collected as it goes, so its PEAK heap must
// not grow with the input — reading the 4.8 MB input whole, or buffering
// the output, would.
func TestShardStream_FilterSingleFileMemoryBounded(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	const small, large = 40_000, 400_000 // 12-byte records
	fixed := uint64(2 << 20)
	for _, arm := range []string{"single", "anchored"} {
		for _, expr := range []string{"", "score >= 0.0"} {
			t.Run(fmt.Sprintf("%s/predicate=%v", arm, expr != ""), func(t *testing.T) {
				var got [2]uint64
				for i, recs := range []int{small, large} {
					svc, fsys, dir := streamOsEnv(t)
					single, anchored := filterStreamFixture(t, svc, fsys, dir, recs)
					src := single
					if arm == "anchored" {
						src = anchored
					}
					dst := filepath.Join(dir, "out.pulse")
					run := func() error {
						out, err := svc.FilterToFileWithOptions(context.Background(), src, dst,
							FilterToFileOptions{Expression: expr, Flatten: expr == ""})
						if err == nil && out.Records != int64(recs) {
							return fmt.Errorf("wrote %d records, want %d", out.Records, recs)
						}
						return err
					}
					if expr == "" {
						got[i] = allocDuring(t, run)
					} else {
						got[i] = peakHeapDuring(t, run)
					}
					assertNoStages(t, fsys, dir)
				}
				t.Logf("%d rows %d B, %d rows %d B", small, got[0], large, got[1])
				if expr == "" {
					for i, g := range got {
						if g > fixed {
							t.Errorf("predicate-free copy #%d allocated %d B, want <= %d (fixed buffers)", i, g, fixed)
						}
					}
				}
				if got[1] > got[0]+uint64(small*12) {
					t.Errorf("memory grows with the input: %d B at %d rows, %d B at %d", got[0], small, got[1], large)
				}
			})
		}
	}
}
