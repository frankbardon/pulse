//go:build perf

package service

import (
	"fmt"
	"runtime"
	"testing"
)

// parallelDecodePerfRatio is the upper bound on (parallel ns/op) /
// (serial ns/op). 0.67 ⇒ parallel must shave at least 33 % of the
// serial wall-clock. Numbers tighter than 0.5 are achievable on
// fully-warm caches with high core counts but flake under contention;
// 0.67 is the regression-resistant gate the story specifies.
const parallelDecodePerfRatio = 0.67

func TestParallelDecode_PerfGate(t *testing.T) {
	numCPU := runtime.NumCPU()
	if numCPU < 2 {
		t.Skipf("perf gate requires NumCPU >= 2 (got %d) — NumCPU variant collapses to serial",
			numCPU)
	}

	// Helper that drives one sub-case via testing.Benchmark and returns
	// the converged metrics. The sub-case name is the exact sub-test
	// path BenchmarkBufferedProcessWideCohortMergeable's b.Run uses.
	runSub := func(workers int) testing.BenchmarkResult {
		var subName string
		switch workers {
		case 1:
			subName = "workers=1"
		default:
			subName = fmt.Sprintf("workers=numcpu=%d", workers)
		}
		t.Logf("perf gate: running sub-case %q", subName)
		res := testing.Benchmark(func(b *testing.B) {
			// Filter the parent bench down to the target sub-case by
			// matching b.Name() suffix. testing.Benchmark drives the
			// whole bench function, so we restrict to one sub-case by
			// checking the parent b.Name and rejecting the others — but
			// testing.Benchmark does not honour -bench filter inside a
			// closure, so we instead invoke the sub-case body directly.
			runMergeableBench(b, workers)
		})
		return res
	}

	serial := runSub(1)
	parallel := runSub(numCPU)

	if serial.NsPerOp() == 0 {
		t.Fatalf("serial sub-case reported 0 ns/op (NsPerOp=%d, N=%d) — bench did not converge",
			serial.NsPerOp(), serial.N)
	}
	if parallel.NsPerOp() == 0 {
		t.Fatalf("parallel sub-case reported 0 ns/op (NsPerOp=%d, N=%d) — bench did not converge",
			parallel.NsPerOp(), parallel.N)
	}

	ratio := float64(parallel.NsPerOp()) / float64(serial.NsPerOp())

	t.Logf("perf gate: workers=1 ns/op=%d B/op=%d allocs/op=%d",
		serial.NsPerOp(), serial.AllocedBytesPerOp(), serial.AllocsPerOp())
	t.Logf("perf gate: workers=numcpu=%d ns/op=%d B/op=%d allocs/op=%d",
		numCPU, parallel.NsPerOp(), parallel.AllocedBytesPerOp(), parallel.AllocsPerOp())
	t.Logf("perf gate: ratio (parallel/serial) = %.3f (threshold %.3f)",
		ratio, parallelDecodePerfRatio)

	if ratio > parallelDecodePerfRatio {
		t.Fatalf("parallel decode perf gate FAILED: ratio %.3f > threshold %.3f "+
			"(workers=%d ns/op=%d vs workers=1 ns/op=%d). "+
			"Either parallel-decode has regressed or the host CPU count is too "+
			"constrained to show the win; re-run with -count=3 on a quiet host "+
			"before concluding regression.",
			ratio, parallelDecodePerfRatio,
			numCPU, parallel.NsPerOp(), serial.NsPerOp())
	}
}
