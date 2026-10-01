package synth

// Float fusion and cross-architecture reproducibility.
//
// The Go spec permits an implementation to contract `a + b*c` (and
// `c - a*b`) into a single fused multiply-add, rounding once instead of
// twice. The arm64 backend does this; the amd64 backend does not. The
// same source therefore yields different last bits on different CPUs,
// and `pulse profile create` promises a byte-identical document for a
// given cohort — a promise that would otherwise hold per-machine only,
// which is worthless for an artefact users commit, share and diff.
//
// The only thing in the language that forbids contraction is an
// EXPLICIT floating-point conversion: `a + float64(b*c)` must round the
// product before the add. Assigning the product to a local does NOT
// help — the spec allows fusion across statements, which is why
// `scaled := f * mult; scaled - 0.5` fuses just as readily as the
// one-liner. Every `float64(...)` wrapped around a product in this
// package is that barrier and is load-bearing; removing one as
// "redundant" silently un-fixes a whole architecture.
//
// `math.FMA` is the opposite lever — portable, but it FORCES fusion
// everywhere, changing amd64's values rather than preserving them. The
// barrier is preferred because it keeps the arithmetic every existing
// document was captured with.
//
// The barriers are gated by moments_internal_test.go, which compares
// each real formula against an explicitly-unfused twin. That gate can
// only DETECT a regression on a fusing architecture (arm64), so it is
// silent on amd64 by construction.
//
// Two known residuals this cannot reach:
//
//   - `math.Exp` (assembly on both amd64 and arm64) and `math.Log`
//     (assembly on amd64, pure Go on arm64) are architecture-specific
//     in the standard library. A `--fit-shape` capture and a lognormal
//     draw ride them, so those remain machine-sensitive in their last
//     bits. `math.Sqrt` is exempt — IEEE-754 requires it to be
//     correctly rounded.
//   - `processing/regression` (the OLS engine behind the `models`
//     section) has not been made fusion-free; see the package's own
//     notes.

// sampleVariance is the unbiased sample variance from a running
// (count, sum, sumSq) triple, in the one place every caller shares.
//
// Two properties are deliberate and must not be "tidied":
//
//   - `float64(mean * sum)` is the FMA barrier described above. Without
//     it this single expression is the difference between a profile
//     document that reproduces across machines and one that does not.
//   - The negative floor is a SILENT correction for catastrophic
//     cancellation: this form loses log10(1 + mean²/variance)
//     significant digits, so a low-variance/large-mean field can
//     produce a small negative here and be reported as having no
//     spread. That hazard is logged as a separate concern; converting
//     to Welford changes expected values across every captured
//     document and is not a fusion fix.
func sampleVariance(count int, sum, sumSq float64) float64 {
	if count < 2 {
		return 0
	}
	mean := sum / float64(count)
	variance := (sumSq - float64(mean*sum)) / float64(count-1)
	if variance < 0 {
		variance = 0
	}
	return variance
}
