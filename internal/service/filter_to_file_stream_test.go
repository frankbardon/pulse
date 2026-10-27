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
	t.Parallel()

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
