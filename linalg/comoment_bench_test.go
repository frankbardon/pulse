package linalg_test

import (
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/linalg"
)

// benchRows is a fixed weighted sample with a sprinkling of missing
// values, shared by the CoMoment benchmarks.
func benchRows(n, p int) []wrow {
	return randomRows(rand.New(rand.NewPCG(99, 100)), n, p)
}

// BenchmarkCoMomentAdd measures the per-row fold in each mode.
func BenchmarkCoMomentAdd(b *testing.B) {
	const p = 8
	rows := benchRows(4096, p)
	for _, mode := range bothModes {
		b.Run(mode.String(), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				c, _ := linalg.NewCoMoment(p, mode)
				for _, r := range rows {
					c.Add(r.x, r.w)
				}
			}
		})
	}
}

// BenchmarkCoMomentMerge measures the fixed merge tree over blocks.
func BenchmarkCoMomentMerge(b *testing.B) {
	const p = 8
	for _, mode := range bothModes {
		parts := make([]*linalg.CoMoment, 64)
		for i := range parts {
			parts[i], _ = linalg.NewCoMoment(p, mode)
			for _, r := range benchRows(64, p) {
				parts[i].Add(r.x, r.w)
			}
		}
		b.Run(mode.String(), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := linalg.MergeTree(parts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
