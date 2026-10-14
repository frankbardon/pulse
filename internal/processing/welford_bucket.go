// welfordBucket carries the streaming Welford-Pébaÿ recurrence (mean + M2 + count) reused across statistical aggregators and tests.
package processing

import "math"

// welfordBucket maintains the running (n, mean, M2) state needed for an
// online single-pass computation of mean and sample variance via the
// Welford-Pébaÿ recurrence. Consumers include TEST_T / TEST_WELCH (in
// internal/processing/test_t.go) and AGG_WELFORD (in internal/processing/aggregator_welford.go);
// the shared type guarantees the two surfaces use the exact same
// recurrence and therefore produce byte-equal moments for equivalent
// input streams.
type welfordBucket struct {
	n    int64
	mean float64
	m2   float64
}

func (b *welfordBucket) add(v float64) {
	b.n++
	delta := v - b.mean
	b.mean += delta / float64(b.n)
	delta2 := v - b.mean
	b.m2 += delta * delta2
}

// sampleVariance returns the unbiased sample variance s² = M2 / (n - 1).
// Undefined for n < 2 — NaN (JSON null), never 0 (E4-S6); a caller that
// needs a sample variance checks n first.
func (b *welfordBucket) sampleVariance() float64 {
	if b.n < 2 {
		return math.NaN()
	}
	return b.m2 / float64(b.n-1)
}
