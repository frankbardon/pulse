//go:build heavy

package io

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
)

// TestTransfer_BoundedMemory: compress and decompress stream — the peak
// heap of one pass over a ~12x larger file stays under 2x the smaller
// file's (it is the codec window plus fixed buffers, not the file). The
// large file is the real cohort's bytes followed by eleven copies of its
// record region: transfer is byte-agnostic past the magic, and a file
// several times larger than the peak is what makes the ratio meaningful.
// Minimum of three runs per side, compared as a ratio.
func TestTransfer_BoundedMemory(t *testing.T) {
	ctx := context.Background()
	osfs := afero.NewOsFs()
	dir := t.TempDir()
	small := importJoinShape(t, dir, "small", 120_000, false, false)
	raw, err := os.ReadFile(small)
	if err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(dir, "large.pulse")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(raw)
	for i := 0; i < 11; i++ {
		_, _ = f.Write(raw[64<<10:])
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	raw = nil
	minPeak := func(fn func() error) float64 {
		best := -1.0
		for i := 0; i < 3; i++ {
			p, err := transferPeakHeap(fn)
			if err != nil {
				t.Fatal(err)
			}
			if best < 0 || p < best {
				best = p
			}
		}
		return best
	}
	peaks := map[string][2]float64{}
	for i, src := range []string{small, large} {
		c := minPeak(func() error {
			_, err := (&TransferExportJob{FS: osfs, Source: src, Output: src + ".zst"}).Run(ctx)
			return err
		})
		d := minPeak(func() error {
			_, err := (&TransferImportJob{FS: osfs, Source: src + ".zst", Output: src + ".rt", Overwrite: true}).Run(ctx)
			return err
		})
		p := peaks["compress"]
		p[i] = c
		peaks["compress"] = p
		p = peaks["decompress"]
		p[i] = d
		peaks["decompress"] = p
	}
	ss, _ := os.Stat(small)
	ls, _ := os.Stat(large)
	for side, p := range peaks {
		t.Logf("%s peak heap: %.1f MB file -> %.2f MB, %.1f MB file -> %.2f MB", side,
			float64(ss.Size())/(1<<20), p[0]/(1<<20), float64(ls.Size())/(1<<20), p[1]/(1<<20))
		if p[1] > 2*p[0]+(1<<20) {
			t.Fatalf("%s peak heap grew with the file: %.2f MB -> %.2f MB", side, p[0]/(1<<20), p[1]/(1<<20))
		}
		if p[1] > float64(ls.Size())/2 {
			t.Fatalf("%s peak heap %.2f MB is not small against the %.1f MB file", side, p[1]/(1<<20), float64(ls.Size())/(1<<20))
		}
	}
}
