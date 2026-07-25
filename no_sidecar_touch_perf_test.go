//go:build perf

package pulse

import "testing"

// sidecarPresencePerfRatio is the upper bound on
// (with_sidecar_index ns/op) / (no_sidecar_index ns/op). 1.5 gives a
// generous 50% headroom over parity — the two variants should be
// wall-clock indistinguishable by construction (Process never opens
// the sidecar; see TestNoSidecarTouch_ProcessSampleFacetNeverAccessTheIndex
// and TestNoRegression_ProcessAccessCountUnaffectedByIndexPresence for
// the enforceable, non-flaky forms of this invariant), so any ratio
// approaching this bound signals a real behavioral regression rather
// than host noise.
//
// This gate is intentionally NOT wired into any CI workflow — a
// strict wall-clock threshold on a shared CI runner is flaky, exactly
// the same rationale service/parallel_decode_perf_test.go documents
// for its own `-tags=perf` opt-in gate (grep the repo: no workflow
// passes `-tags=perf`). Run manually with:
//
//	go test -tags=perf -run TestSidecarPresence_PerfGate .
//
// The load-bearing, CI-enforced half of the "zero regression"
// requirement is the deterministic access-count assertion in
// no_sidecar_touch_bench_test.go, which runs unconditionally under
// `go test ./...`.
const sidecarPresencePerfRatio = 1.5

func TestSidecarPresence_PerfGate(t *testing.T) {
	noIndex := testing.Benchmark(func(b *testing.B) {
		runSidecarPresenceBench(b, false)
	})
	withIndex := testing.Benchmark(func(b *testing.B) {
		runSidecarPresenceBench(b, true)
	})

	if noIndex.NsPerOp() == 0 {
		t.Fatalf("no_sidecar_index sub-case reported 0 ns/op (N=%d) — bench did not converge", noIndex.N)
	}
	if withIndex.NsPerOp() == 0 {
		t.Fatalf("with_sidecar_index sub-case reported 0 ns/op (N=%d) — bench did not converge", withIndex.N)
	}

	ratio := float64(withIndex.NsPerOp()) / float64(noIndex.NsPerOp())

	t.Logf("perf gate: no_sidecar_index   ns/op=%d B/op=%d allocs/op=%d",
		noIndex.NsPerOp(), noIndex.AllocedBytesPerOp(), noIndex.AllocsPerOp())
	t.Logf("perf gate: with_sidecar_index ns/op=%d B/op=%d allocs/op=%d",
		withIndex.NsPerOp(), withIndex.AllocedBytesPerOp(), withIndex.AllocsPerOp())
	t.Logf("perf gate: ratio (with/without) = %.3f (threshold %.3f)", ratio, sidecarPresencePerfRatio)

	if ratio > sidecarPresencePerfRatio {
		t.Fatalf("sidecar-presence perf gate FAILED: ratio %.3f > threshold %.3f "+
			"(with_sidecar_index ns/op=%d vs no_sidecar_index ns/op=%d). "+
			"Process should never be slowed by an unrelated sidecar file's presence; "+
			"re-run with -count=3 on a quiet host before concluding regression.",
			ratio, sidecarPresencePerfRatio, withIndex.NsPerOp(), noIndex.NsPerOp())
	}
}
