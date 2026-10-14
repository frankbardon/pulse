package linalg_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/linalg"
)

// benchRows is a fixed weighted sample shared by the CoMoment
// benchmarks: correlated columns, positive weights and ~1/(4p) of cells
// missing, so about a fifth of rows are incomplete at every p. (The
// tests' randomRows misses 15% of cells, which at p = 50 leaves listwise
// almost no complete row to fold, timing the drop instead of the work.)
// Products carry explicit float64 barriers so arm64 FMA fusion cannot
// move the inputs.
func benchRows(n, p int) []wrow {
	r := rand.New(rand.NewPCG(99, 100))
	missing := 0.25 / float64(p)
	rows := make([]wrow, n)
	for i := range rows {
		x := make([]float64, p)
		base := r.NormFloat64()
		for j := range x {
			if r.Float64() < missing {
				x[j] = math.NaN()
				continue
			}
			x[j] = float64(0.6*base) + float64(0.8*r.NormFloat64())
		}
		rows[i] = wrow{x: x, w: 0.5 + r.Float64()}
	}
	return rows
}

// coMomentBenchShapes are the dimensions the CoMoment benchmarks run:
// p = 8 (the original case), and p = 50 / p = 256, the scale tiers of
// the engine-level matrix benchmarks (internal/service
// matrix_scale_bench_test.go). rows feeds the per-row fold; parts is
// the merge tree's block count. Both shrink with p so the p = 256
// pairwise case (32,896 pair states, 2.4 MB each accumulator) stays at
// seconds.
var coMomentBenchShapes = []struct {
	p, rows, parts int
}{
	{8, 4096, 64},
	{50, 4096, 64},
	{256, 512, 16},
}

// BenchmarkCoMomentAdd measures the per-row fold in each mode.
func BenchmarkCoMomentAdd(b *testing.B) {
	for _, sh := range coMomentBenchShapes {
		rows := benchRows(sh.rows, sh.p)
		for _, mode := range bothModes {
			b.Run(fmt.Sprintf("p=%d/%s", sh.p, mode), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					c, _ := linalg.NewCoMoment(sh.p, mode)
					for _, r := range rows {
						c.Add(r.x, r.w)
					}
				}
			})
		}
	}
}

// BenchmarkCoMomentMerge measures the fixed merge tree over blocks.
func BenchmarkCoMomentMerge(b *testing.B) {
	for _, sh := range coMomentBenchShapes {
		for _, mode := range bothModes {
			parts := make([]*linalg.CoMoment, sh.parts)
			for i := range parts {
				parts[i], _ = linalg.NewCoMoment(sh.p, mode)
				for _, r := range benchRows(64, sh.p) {
					parts[i].Add(r.x, r.w)
				}
			}
			b.Run(fmt.Sprintf("p=%d/%s", sh.p, mode), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, err := linalg.MergeTree(parts); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
