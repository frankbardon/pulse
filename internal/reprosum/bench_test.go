package reprosum

import (
	"encoding/binary"
	"math"
	"testing"
)

// sink keeps the compiler from discarding a benchmark's result.
var sink float64

// naiveState is naive summation the way an engine aggregator holds it: a
// float64 field behind a pointer, one non-inlined call per value.
type naiveState struct{ sum float64 }

//go:noinline
func (n *naiveState) Add(x float64) { n.sum += x }

// binnedState wraps Sum behind the same non-inlined call shape.
type binnedState struct{ s Sum }

//go:noinline
func (n *binnedState) Add(x float64) { n.s.Add(x) }

// benchValues is an in-cache working set: the pure kernel cost, the worst
// case for the binned sum against a naive add.
func benchValues() []float64 { return signedUniformCorpus(42, 4096) }

// BenchmarkReproSum_Add is the kernel alone against naive +=, on an
// in-cache slice (no memory traffic hides the extra flops). naive and
// binned run inline in the loop; naive_state and binned_state go through
// a non-inlined pointer-receiver call, the shape an engine aggregator has.
func BenchmarkReproSum_Add(b *testing.B) {
	xs := benchValues()
	b.Run("naive", func(b *testing.B) {
		var s float64
		for i := 0; i < b.N; i++ {
			for _, x := range xs {
				s += x
			}
		}
		sink = s
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(xs)), "ns/add")
	})
	b.Run("binned", func(b *testing.B) {
		var s Sum
		for i := 0; i < b.N; i++ {
			for _, x := range xs {
				s.Add(x)
			}
		}
		sink = s.Float64()
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(xs)), "ns/add")
	})
	b.Run("naive_state", func(b *testing.B) {
		n := new(naiveState)
		for i := 0; i < b.N; i++ {
			for _, x := range xs {
				n.Add(x)
			}
		}
		sink = n.sum
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(xs)), "ns/add")
	})
	b.Run("binned_state", func(b *testing.B) {
		n := new(binnedState)
		for i := 0; i < b.N; i++ {
			for _, x := range xs {
				n.Add(x)
			}
		}
		sink = n.s.Float64()
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(xs)), "ns/add")
	})
}

func BenchmarkReproSum_Merge(b *testing.B) {
	p, q := sumOf(mixedCorpus(1, 100)), sumOf(mixedCorpus(2, 100))
	for i := 0; i < b.N; i++ {
		c := p
		c.Merge(&q)
		sink = c.pri[0]
	}
}

// Raw-scan benches. Rows are fixed-width like .pulse records: a 64-byte
// stride, a u32 group key at offset 0 and an f64 measure at offset 8,
// read with encoding/binary. The buffer (2^21 rows, 128 MiB) is larger
// than the last-level cache. There is no per-row engine work, and the
// naive arm keeps its sum in a register, which no engine aggregator does,
// so these are the kernel's worst case, not the end-to-end figure: that is
// BenchmarkReproSum_EngineProxy (internal/processing).
const (
	proxyStride = 64
	proxyRows   = 1 << 21
	proxyGroups = 800_000
)

func proxyBuffer() []byte {
	r := newRand(7)
	buf := make([]byte, proxyRows*proxyStride)
	for i := range proxyRows {
		row := buf[i*proxyStride:]
		binary.LittleEndian.PutUint32(row, uint32(r.IntN(proxyGroups)))
		binary.LittleEndian.PutUint64(row[8:], math.Float64bits(signed(r, math.Ldexp(r.Float64(), r.IntN(20)))))
	}
	return buf
}

// BenchmarkReproSum_RawScan is the cohort-wide sum over the raw row scan.
func BenchmarkReproSum_RawScan(b *testing.B) {
	buf := proxyBuffer()
	b.Run("naive", func(b *testing.B) {
		var s float64
		for i := 0; i < b.N; i++ {
			for off := 0; off < len(buf); off += proxyStride {
				s += math.Float64frombits(binary.LittleEndian.Uint64(buf[off+8:]))
			}
		}
		sink = s
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*proxyRows), "ns/row")
	})
	b.Run("binned", func(b *testing.B) {
		var s Sum
		for i := 0; i < b.N; i++ {
			for off := 0; off < len(buf); off += proxyStride {
				s.Add(math.Float64frombits(binary.LittleEndian.Uint64(buf[off+8:])))
			}
		}
		sink = s.Float64()
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*proxyRows), "ns/row")
	})
}

// BenchmarkReproSum_RawScanGrouped is the same scan accumulating into one
// state per group (800k groups, random keys): a 56-byte Sum per group
// against an 8-byte float64, so it also prices the 7x state footprint.
func BenchmarkReproSum_RawScanGrouped(b *testing.B) {
	buf := proxyBuffer()
	b.Run("naive", func(b *testing.B) {
		acc := make([]float64, proxyGroups)
		for i := 0; i < b.N; i++ {
			for off := 0; off < len(buf); off += proxyStride {
				g := binary.LittleEndian.Uint32(buf[off:])
				acc[g] += math.Float64frombits(binary.LittleEndian.Uint64(buf[off+8:]))
			}
		}
		sink = acc[0]
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*proxyRows), "ns/row")
	})
	b.Run("binned", func(b *testing.B) {
		acc := make([]Sum, proxyGroups)
		for i := 0; i < b.N; i++ {
			for off := 0; off < len(buf); off += proxyStride {
				g := binary.LittleEndian.Uint32(buf[off:])
				acc[g].Add(math.Float64frombits(binary.LittleEndian.Uint64(buf[off+8:])))
			}
		}
		sink = acc[0].Float64()
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*proxyRows), "ns/row")
	})
}
