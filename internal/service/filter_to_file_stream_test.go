package service

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// U51 E4-S5: the single-file and anchored filter-to-file arms stream
// rows through dst's stage (`<dst>.pulse.tmp-staged`) and publish by one
// rename — memory O(the copy window), dst untouched until the rename.
// The byte twin over every layout is the root
// TestShardStream_FilterSingleFileByteTwin.

// filterStreamFixture writes a single file of recs score rows and an
// archive holding it as shard "one.pulse".
func filterStreamFixture(t *testing.T, svc *Service, fsys afero.Fs, dir string, recs int) (single, anchored string) {
	t.Helper()
	single = filepath.Join(dir, "one.pulse")
	adminWriteShardFile(t, fsys, single, adminScoreSchema(), streamShardRecords(0, recs))
	archive := filepath.Join(dir, "arch.pulse")
	if _, err := svc.CreateShardArchive(context.Background(), archive, []string{single}); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return single, archive + "#one.pulse"
}

// TestShardStream_FilterSingleFileFailureLeavesDestination: a run whose
// stage write, fsync or publishing rename fails (an injected afero.Fs
// fault) fails, leaves an existing destination byte-identical and an
// absent one absent, and leaves no stage behind — on the single-file
// and the anchored arm, with a predicate and without.
func TestShardStream_FilterSingleFileFailureLeavesDestination(t *testing.T) {
	for _, arm := range []string{"single", "anchored"} {
		for _, stage := range []string{"write", "sync", "rename"} {
			for _, expr := range []string{"score >= 0.0", ""} {
				for _, prior := range []bool{true, false} {
					name := fmt.Sprintf("%s/%s/predicate=%v/prior=%v", arm, stage, expr != "", prior)
					t.Run(name, func(t *testing.T) {
						base := afero.NewMemMapFs()
						seed, err := fs.New(fs.WithFs(base))
						if err != nil {
							t.Fatal(err)
						}
						// 40k rows: the stage crosses several copy windows.
						single, anchored := filterStreamFixture(t, New(seed), base, ".", 40_000)
						src := single
						if arm == "anchored" {
							src = anchored
						}
						old := []byte("previous output")
						if prior {
							if err := afero.WriteFile(base, "dst.pulse", old, 0o644); err != nil {
								t.Fatal(err)
							}
						}
						cfg, err := fs.New(fs.WithFs(faultFs{Fs: base, stage: stage}))
						if err != nil {
							t.Fatal(err)
						}
						_, err = New(cfg).FilterToFileWithOptions(context.Background(), src, "dst.pulse",
							FilterToFileOptions{Expression: expr, Flatten: expr == ""})
						if err == nil {
							t.Fatal("a run with an injected fault succeeded")
						}
						got, rerr := afero.ReadFile(base, "dst.pulse")
						switch {
						case prior && (rerr != nil || !bytes.Equal(got, old)):
							t.Fatalf("a failed run touched the destination (%d B, %v)", len(got), rerr)
						case !prior && rerr == nil:
							t.Fatalf("a failed run left a %d B destination", len(got))
						}
						assertNoStages(t, base, ".")
					})
				}
			}
		}
	}
}

// TestShardStream_FilterSingleFileMemoryBounded: the single-file and
// anchored arms hold the copy window, never the input or the output.
// The same run over 40k and 400k rows (10x the bytes) is measured: a
// predicate-free copy ALLOCATES fixed buffers only; a filtered run's
// per-row decode garbage is collected as it goes, so its PEAK heap must
// not grow with the input — reading the 4.8 MB input whole, or buffering
// the output, would.
func TestShardStream_FilterSingleFileMemoryBounded(t *testing.T) {
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
