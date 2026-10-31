package regression

import "math"

// olsQR is a streaming QR factorization of the weighted design: each
// row √w·[1, x₁ − x₁⁰ … x_p − x_p⁰, y − y⁰] is rotated into an
// upper-triangular R of order p+2 by Givens rotations (Miller 1992,
// AS 274; the method behind R's biglm). The origin (x⁰, y⁰) is the
// first row: least squares is shift-invariant in its slopes, residuals
// and explained sum of squares, and a column held at the data's
// magnitude (a predictor near 1e6 with a spread of 10) would otherwise
// sit nearly parallel to the intercept column and cost the slopes
// digits. After the stream, the leading (p+1)×(p+1) block R_x solves
// R_x·β = R[0:p+1, p+1] (β₀ on the shifted scale; the caller reads the
// intercept from the means instead), the last diagonal entry is the
// residual norm, RSS = R[p+1, p+1]², and Σ_{j=1..p} R[j, p+1]² is the
// explained sum of squares about the mean.
//
// Why: solving the normal equations (Cholesky on the Gram matrix XᵀX)
// squares the design's condition number. On an ill-conditioned design
// (the NIST StRD Wampler polynomials, Filip's degree-10 polynomial) that
// cost the coefficients half their digits or refused the fit outright.
// An orthogonal factorization works on the design itself, at
// κ(X) rather than κ(X)², for the same O(p²) per row and O(p²) memory, so
// the fit still streams.
//
// FMA-free: every product is an explicit float64(...) so arm64 cannot
// contract it into a fused multiply-add (TestOLSQR_NoFMAContraction);
// the rotation's hypotenuse is computed here, not by math.Hypot, whose
// pure-Go arm64 body is contractible.
type olsQR struct {
	m int // lead + p + 1: [intercept,] the p predictors, the target
	// lead is 1 when the design carries the intercept column, 0 for a
	// fit through the origin (RegressionSpec.Intercept false), which
	// rotates the raw √w·[x, y] with no column of ones and no origin
	// shift (a shift is only invariant with an intercept).
	lead int
	r    []float64 // m×m upper triangle, row-major
	// colSq[j] is Σ w·x_j² of design column j (intercept first): the
	// scale the rank test reads R's diagonal against.
	colSq []float64
	row   []float64 // per-row scratch
	// origin is the first row's [x₁ … x_p, y]; set marks it fixed.
	origin []float64
	set    bool
}

func newOLSQR(p int) *olsQR { return newOLSQRWith(p, true) }

// newOLSQRWith is newOLSQR with the intercept column present or not.
func newOLSQRWith(p int, intercept bool) *olsQR {
	lead := 0
	if intercept {
		lead = 1
	}
	m := lead + p + 1
	q := &olsQR{m: m, lead: lead, r: make([]float64, m*m), colSq: make([]float64, m-1), row: make([]float64, m), origin: make([]float64, p+1)}
	// Through the origin the origin stays zero: no shift.
	q.set = !intercept
	return q
}

// add rotates one row, weight w > 0, into R.
func (q *olsQR) add(x []float64, y, w float64) {
	if !q.set {
		copy(q.origin, x)
		q.origin[len(x)] = y
		q.set = true
	}
	sw := math.Sqrt(w)
	if q.lead == 1 {
		q.row[0] = sw
	}
	for j, v := range x {
		q.row[q.lead+j] = float64(sw * (v - q.origin[j]))
	}
	q.row[q.m-1] = float64(sw * (y - q.origin[len(x)]))
	for j := 0; j < q.m-1; j++ {
		q.colSq[j] += float64(q.row[j] * q.row[j])
	}
	for j := 0; j < q.m; j++ {
		b := q.row[j]
		if b == 0 {
			continue
		}
		a := q.r[j*q.m+j]
		h := hypotNoFMA(a, b)
		c, s := a/h, b/h
		q.r[j*q.m+j] = h
		for k := j + 1; k < q.m; k++ {
			t := q.r[j*q.m+k]
			v := q.row[k]
			q.r[j*q.m+k] = float64(c*t) + float64(s*v)
			q.row[k] = float64(c*v) - float64(s*t)
		}
	}
}

// hypotNoFMA is √(a² + b²) without overflow or contraction.
func hypotNoFMA(a, b float64) float64 {
	a, b = math.Abs(a), math.Abs(b)
	if a < b {
		a, b = b, a
	}
	if a == 0 {
		return 0
	}
	t := b / a
	return float64(a * math.Sqrt(1+float64(t*t)))
}

// qrRankTol is the relative size below which a diagonal entry of R_x
// marks its column as (numerically) a linear combination of the earlier
// ones: |R_jj| ≤ qrRankTol·‖x_j‖. It is the double-precision floor
// (a few hundred ulps), so an exactly collinear or constant predictor,
// whose |R_jj| is rounding residue near 1e-16·‖x_j‖, is refused, while
// an ill-conditioned but full-rank design such as Filip is fitted.
const qrRankTol = 1e-13

// solve returns the coefficients [β₀′, β₁ … β_p] (β₀′ on the shifted
// scale), the residual and explained sums of squares, and the inverse
// of R_x (upper triangular, (p+1)×(p+1), row-major), so the shifted
// design's (XᵀWX)⁻¹ = R_x⁻¹·R_x⁻ᵀ, whose slope block is the centred
// M2_xx⁻¹. ok is false when R_x is rank-deficient by qrRankTol.
func (q *olsQR) solve() (beta []float64, rss, ess float64, rinv []float64, ok bool) {
	n := q.m - 1
	for j := 0; j < n; j++ {
		d := math.Abs(q.r[j*q.m+j])
		if !(d > float64(qrRankTol*math.Sqrt(q.colSq[j]))) {
			return nil, 0, 0, nil, false
		}
	}
	beta = make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := q.r[i*q.m+n]
		for k := i + 1; k < n; k++ {
			s -= float64(q.r[i*q.m+k] * beta[k])
		}
		beta[i] = s / q.r[i*q.m+i]
	}
	last := q.r[n*q.m+n]
	rss = float64(last * last)
	// Explained sum of squares: about the mean with an intercept (its
	// row 0 carries ȳ), uncentred Σŷ² through the origin.
	for j := q.lead; j < n; j++ {
		v := q.r[j*q.m+n]
		ess += float64(v * v)
	}
	// R_x⁻¹ by back substitution, column by column.
	rinv = make([]float64, n*n)
	for j := 0; j < n; j++ {
		rinv[j*n+j] = 1 / q.r[j*q.m+j]
		for i := j - 1; i >= 0; i-- {
			s := 0.0
			for k := i + 1; k <= j; k++ {
				s += float64(q.r[i*q.m+k] * rinv[k*n+j])
			}
			rinv[i*n+j] = -s / q.r[i*q.m+i]
		}
	}
	return beta, rss, ess, rinv, true
}

// covInverse returns (XᵀWX)⁻¹[i][j] = Σ_k R_x⁻¹[i][k]·R_x⁻¹[j][k] from
// solve's rinv (order n = p+1, upper triangular).
func covInverse(rinv []float64, n int) []float64 {
	c := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			s := 0.0
			for k := j; k < n; k++ {
				s += float64(rinv[i*n+k] * rinv[j*n+k])
			}
			c[i*n+j], c[j*n+i] = s, s
		}
	}
	return c
}
