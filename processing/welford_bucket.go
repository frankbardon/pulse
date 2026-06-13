// welfordBucket carries the streaming Welford-Pébaÿ recurrence (mean + M2 + count) reused across statistical aggregators and tests.
package processing

// welfordBucket maintains the running (n, mean, M2) state needed for an
// online single-pass computation of mean and sample variance via the
// Welford-Pébaÿ recurrence. Consumers include TEST_T / TEST_WELCH (in
// processing/test_t.go) and AGG_WELFORD (in processing/aggregator_welford.go);
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
// Returns zero for n < 2 so callers can detect degeneracy themselves.
func (b *welfordBucket) sampleVariance() float64 {
	if b.n < 2 {
		return 0
	}
	return b.m2 / float64(b.n-1)
}
