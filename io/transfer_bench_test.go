package io

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/afero"
)

// Transport-compression measurements over synthetic join-shaped cohorts.
//
// Shape: a denormalised parent/child join — a parent key with a 12-wide
// parent block (categoricals, integers, a float, a nullable column) that
// repeats on every child row, plus 8 child columns — at a fanout of 12.
// "sorted" orders rows by parent key; "scattered" permutes the parent
// per row. "grouped" imports the same rows with the parent block
// declared as a parent group (format 0x02), so each distinct block is
// stored once. Synthetic distributions: the ratios are structural, the
// absolute figures are not comparable to any real cohort.

const (
	transferBenchRows   = 240_000
	transferBenchFanout = 12
)

func joinShapeRows(n int, sorted bool) ([]string, [][]string) {
	cols := []string{"pid", "p_region", "p_segment", "p_channel", "p_tier", "p_country", "p_band", "p_score", "p_weight", "p_age", "p_size", "p_rank", "p_note",
		"line", "sku", "qty", "amount", "status", "flag", "day", "bucket"}
	parents := n / transferBenchFanout
	regions := []string{"north", "south", "east", "west", "central"}
	segs := []string{"retail", "wholesale", "online", "partner"}
	var rows [][]string
	for i := 0; i < n; i++ {
		pid := i / transferBenchFanout
		if !sorted {
			pid = (i * 7919) % parents
		}
		note := fmt.Sprintf("n%03d", pid%211)
		if pid%7 == 0 {
			note = ""
		}
		h := uint32(i)*2654435761 + 12345
		rows = append(rows, []string{
			fmt.Sprint(100000 + pid),
			regions[pid%5], segs[pid%4], fmt.Sprintf("ch-%d", pid%9), fmt.Sprint(1 + pid%3),
			fmt.Sprintf("c%02d", pid%37), fmt.Sprintf("b%d", pid%6), fmt.Sprint(pid % 101),
			fmt.Sprintf("%d.%02d", 50+pid%40, pid%100), fmt.Sprint(18 + pid%60), fmt.Sprint(pid % 1000), fmt.Sprint(pid % 500), note,
			fmt.Sprint(i + 1), fmt.Sprintf("sku-%04d", (h>>16)%300), fmt.Sprint(1 + h%20),
			fmt.Sprintf("%d.%02d", h%5000, (h>>8)%100), []string{"open", "shipped", "closed"}[h%3],
			[]string{"true", "false"}[(h>>3)%2], fmt.Sprint(18000 + (h>>5)%365), fmt.Sprint((h >> 7) % 50),
		})
	}
	return cols, rows
}

var joinShapeGroup = []GroupDecl{{Key: []string{"pid"}, Members: []string{"p_region", "p_segment", "p_channel", "p_tier", "p_country", "p_band", "p_score", "p_weight", "p_age", "p_size", "p_rank", "p_note"}}}

// importJoinShape imports one shape into dir and returns its path.
// Disk-backed on purpose: a memfs would count the output artifact on the
// heap and hide the codec's own bounded footprint.
func importJoinShape(tb testing.TB, dir, name string, rows int, sorted, grouped bool) string {
	tb.Helper()
	cols, data := joinShapeRows(rows, sorted)
	path := filepath.Join(dir, name+".pulse")
	job := NewImportJob(newMockReader(cols, data), path)
	job.FS = afero.NewOsFs()
	if grouped {
		job.Groups = joinShapeGroup
	}
	if _, err := job.Run(context.Background()); err != nil {
		tb.Fatal(err)
	}
	return path
}

// transferPeakHeap runs fn once with a goroutine sampling HeapAlloc and
// returns the peak above the post-GC baseline, in bytes.
func transferPeakHeap(fn func() error) (float64, error) {
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	peak.Store(base.HeapAlloc)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		for {
			select {
			case <-done:
				return
			default:
			}
			runtime.ReadMemStats(&ms)
			for {
				cur := peak.Load()
				if ms.HeapAlloc <= cur || peak.CompareAndSwap(cur, ms.HeapAlloc) {
					break
				}
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()
	err := fn()
	close(done)
	wg.Wait()
	if p := peak.Load(); p > base.HeapAlloc {
		return float64(p - base.HeapAlloc), err
	}
	return 0, err
}

// BenchmarkTransfer reports, per shape: ratio (cohort / artifact bytes),
// throughput over the UNCOMPRESSED bytes (MB/s via SetBytes) for compress
// and decompress, and peak-heap-MB for one pass.
//
//	go test ./io/ -run '^$' -bench BenchmarkTransfer -benchtime 3x
func BenchmarkTransfer(b *testing.B) {
	ctx := context.Background()
	osfs := afero.NewOsFs()
	dir := b.TempDir()
	for _, sh := range []struct {
		name            string
		sorted, grouped bool
	}{
		{"flat_sorted", true, false},
		{"flat_scattered", false, false},
		{"grouped_sorted", true, true},
		{"grouped_scattered", false, true},
	} {
		src := importJoinShape(b, dir, sh.name, transferBenchRows, sh.sorted, sh.grouped)
		zst := src + ".zst"
		info, _ := os.Stat(src)
		b.Run(sh.name+"/compress", func(b *testing.B) {
			b.SetBytes(info.Size())
			var rep *TransferReport
			for i := 0; i < b.N; i++ {
				var err error
				if rep, err = (&TransferExportJob{FS: osfs, Source: src, Output: zst}).Run(ctx); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(rep.Ratio, "ratio")
			b.ReportMetric(float64(rep.CohortBytes)/(1<<20), "cohort-MB")
			b.ReportMetric(float64(rep.CompressedBytes)/(1<<20), "artifact-MB")
			peak, err := transferPeakHeap(func() error {
				_, err := (&TransferExportJob{FS: osfs, Source: src, Output: zst}).Run(ctx)
				return err
			})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(peak/(1<<20), "peak-heap-MB")
		})
		b.Run(sh.name+"/decompress", func(b *testing.B) {
			if _, err := (&TransferExportJob{FS: osfs, Source: src, Output: zst}).Run(ctx); err != nil {
				b.Fatal(err)
			}
			out := src + ".rt"
			b.SetBytes(info.Size())
			for i := 0; i < b.N; i++ {
				if _, err := (&TransferImportJob{FS: osfs, Source: zst, Output: out, Overwrite: true}).Run(ctx); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			peak, err := transferPeakHeap(func() error {
				_, err := (&TransferImportJob{FS: osfs, Source: zst, Output: out, Overwrite: true}).Run(ctx)
				return err
			})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(peak/(1<<20), "peak-heap-MB")
		})
	}
}

// TestTransfer_BoundedMemory: compress and decompress stream — the peak
// heap of one pass over a ~12x larger file stays under 2x the smaller
// file's (it is the codec window plus fixed buffers, not the file). The
// large file is the real cohort's bytes followed by eleven copies of its
// record region: transfer is byte-agnostic past the magic, and a file
// several times larger than the peak is what makes the ratio meaningful.
// Minimum of three runs per side, compared as a ratio.
func TestTransfer_BoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("writes ~60 MB of synthetic bytes")
	}
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
