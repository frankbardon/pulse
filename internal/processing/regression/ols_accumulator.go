package regression

// olsAccumulator is a Welford-style streaming accumulator for the
// sufficient statistics needed by ordinary least squares.
//
// Per row (after filter / attribute / feature stages have populated
// derived columns), the accumulator folds the predictor vector and
// target into:
//
//   - n       : observation count
//   - meanX[p]: running predictor means
//   - meanY   : running target mean
//   - m2XX    : centered cross-products Σᵢ (xᵢ − μ_x)(xᵢ − μ_x)ᵀ, p×p
//   - m2XY    : centered cross-products Σᵢ (xᵢ − μ_x)(yᵢ − μ_y), p
//   - m2YY    : centered target sum of squares Σᵢ (yᵢ − μ_y)²
//
// The recurrences use the Welford-Pébaÿ update rules to avoid the
// catastrophic cancellation that naive Σx² − (Σx)²/n suffers on
// ill-conditioned predictors. Each row contributes O(p²) work; total
// memory is O(p²) regardless of n.
//
// After the iterator is exhausted, m2XX is the centered Gram matrix
// (XᵀX_centered) and m2XY is the centered cross-product (Xᵀy_centered).
// The solver consumes these directly without ever materializing X.
type olsAccumulator struct {
	p     int       // predictor count
	n     int       // observation count
	meanX []float64 // length p
	meanY float64
	// m2XX is stored as a flat row-major slice of length p*p. Symmetric
	// by construction; only the upper triangle is updated and the lower
	// triangle is mirrored at Finalize time.
	m2XX []float64
	m2XY []float64 // length p
	m2YY float64

	// xScratch is a per-row buffer of (xᵢ − μ_x(n−1)) deltas, reused to
	// keep the inner loop allocation-free.
	xScratch []float64
}

// newOLSAccumulator constructs a fresh accumulator for p predictors.
func newOLSAccumulator(p int) *olsAccumulator {
	return &olsAccumulator{
		p:        p,
		meanX:    make([]float64, p),
		m2XX:     make([]float64, p*p),
		m2XY:     make([]float64, p),
		xScratch: make([]float64, p),
	}
}

// UpdateRow folds one observation into the running stats. x must have
// length p; y is the target value. Callers are responsible for filtering
// rows where x or y is null before invoking UpdateRow (a null
// contributes nothing to the centered moments and would otherwise bias
// the means).
//
// Update sequence (per Welford):
//
//	n     ← n + 1
//	δx    ← x − meanX (before update)
//	meanX ← meanX + δx / n
//	δx'   ← x − meanX (after update)
//	δy    ← y − meanY (before update)
//	meanY ← meanY + δy / n
//	δy'   ← y − meanY (after update)
//	m2XX += δx ⊗ δx'   (outer product; symmetric)
//	m2XY += δx · δy'
//	m2YY += δy · δy'
//
// The mixed-time-step δx · δx' (not δx · δx) is the trick that keeps the
// recurrence numerically stable.
func (a *olsAccumulator) UpdateRow(x []float64, y float64) {
	a.n++
	nf := float64(a.n)

	// δx (pre-update) into scratch.
	for j := 0; j < a.p; j++ {
		a.xScratch[j] = x[j] - a.meanX[j]
	}
	// Update meanX.
	for j := 0; j < a.p; j++ {
		a.meanX[j] += a.xScratch[j] / nf
	}
	// δy (pre-update).
	dy := y - a.meanY
	a.meanY += dy / nf
	// δy' (post-update).
	dyPost := y - a.meanY

	// m2XX[i][j] += δx_i · (x_j − meanX_j_after) = δx_i · δx_j'.
	// Compute δx' (post-update) into a second use of scratch: but we still
	// need the original δx for the outer product. Use direct (x − meanX)
	// (post-update) inline since meanX is updated.
	for i := 0; i < a.p; i++ {
		dxiPre := a.xScratch[i]
		for j := i; j < a.p; j++ {
			dxjPost := x[j] - a.meanX[j]
			a.m2XX[i*a.p+j] += dxiPre * dxjPost
		}
		// m2XY uses δx_i (pre) and δy' (post-update).
		a.m2XY[i] += dxiPre * dyPost
	}
	a.m2YY += dy * dyPost
}

// finalize mirrors the upper triangle of m2XX into the lower triangle so
// the solver can consume a symmetric p×p matrix. The accumulator is
// safe to re-use after finalize.
func (a *olsAccumulator) finalize() {
	for i := 0; i < a.p; i++ {
		for j := i + 1; j < a.p; j++ {
			a.m2XX[j*a.p+i] = a.m2XX[i*a.p+j]
		}
	}
}
