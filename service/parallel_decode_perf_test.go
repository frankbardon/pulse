//go:build perf

package service

import (
	"fmt"
	"runtime"
	"testing"
)

// parallel_decode_perf_test.go houses the build-tag-gated perf gate for
// the per-cohort parallel decode dispatch. Default CI runs (no -tags)
// skip this file entirely; the maintainer runs it on demand via
//
//	go test -tags=perf ./service/... -run TestParallelDecode_PerfGate -count=3
//
// The gate runs BenchmarkBufferedProcessWideCohortMergeable via
// testing.Benchmark for the workers=1 (serial) and workers=NumCPU
// (parallel) sub-cases, then asserts the parallel variant's ns/op is at
// most parallelDecodePerfRatio of the serial variant's ns/op.
//
// The 0.67 ratio (≤ 67 % of serial wall-clock) is the E3-S5 acceptance
// threshold: a healthy parallel decode on a multi-core machine should
// give a meaningful — though not necessarily linear — speedup. We do not
// chase the theoretical (1/N) bound because the per-record cost is
// dominated by map[string]float64 allocation, which contends on the
// runtime's mcache and amortises sub-linearly with worker count.
//
// Constrained-runner safety: on a host where runtime.NumCPU() == 1 the
// NumCPU variant collapses to the serial path; the ratio is then ~1.0
// and the gate would trivially fail despite there being no regression.
// We Skip with a clear message in that case rather than mask a real
// regression with a soft threshold.
//
// Run cadence: the perf gate is sensitive to machine state (background
// load, thermals, CPU frequency scaling). The maintainer takes the
// median of -count=3 runs; the bench fixture (b.Loop in the underlying
// bench) supplies the per-run iteration count automatically. testing.B
// + testing.Benchmark already runs each sub-case to statistical stability
// before returning, so this gate compares the converged ns/op values.

// parallelDecodePerfRatio is the upper bound on (parallel ns/op) /
// (serial ns/op). 0.67 ⇒ parallel must shave at least 33 % of the
// serial wall-clock. Numbers tighter than 0.5 are achievable on
// fully-warm caches with high core counts but flake under contention;
// 0.67 is the regression-resistant gate the story specifies.
const parallelDecodePerfRatio = 0.67

// TestParallelDecode_PerfGate asserts the perf acceptance criterion for
// the E3 parallel-decode dispatch. It uses testing.Benchmark to drive
// BenchmarkBufferedProcessWideCohortMergeable programmatically at two
// fixed worker counts, compares the resulting ns/op, and fails when the
// NumCPU variant fails to clear parallelDecodePerfRatio of the workers=1
// baseline.
//
// The gate ALSO reports allocs/op and B/op for both variants so a
// regression in per-worker partial state (the E3-S3 reducer holds one
// shardPartial per worker) shows up alongside the wall-clock delta —
// parallel decode should keep allocations near-flat vs serial because
// the per-worker accumulator is a small fixed-size struct, not a copy of
// the cohort.
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
