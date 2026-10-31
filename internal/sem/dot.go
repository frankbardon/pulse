package sem

// dot is Σ x[i]·y[i] (len(y) ≥ len(x)) in the summation order of
// gonum's amd64 assembly floats.Dot (internal/asm/f64 DotUnitary): four
// running sums over i mod 4 for the whole quads, the 1–3 element tail
// into the first, combined as (s0+s2) + (s1+s3). Every product is an
// explicit float64(...) barrier, so arm64 — where gonum's pure-Go Dot is
// contracted into fused multiply-adds — computes the same bits amd64
// always did (U61 cross-arch determinism).
func dot(x, y []float64) float64 {
	var s0, s1, s2, s3 float64
	n := len(x)
	i := 0
	for ; i+4 <= n; i += 4 {
		s0 += float64(x[i] * y[i])
		s1 += float64(x[i+1] * y[i+1])
		s2 += float64(x[i+2] * y[i+2])
		s3 += float64(x[i+3] * y[i+3])
	}
	for ; i < n; i++ {
		s0 += float64(x[i] * y[i])
	}
	return (s0 + s2) + (s1 + s3)
}
