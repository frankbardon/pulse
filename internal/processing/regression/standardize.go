package regression

import "math"

// standardizedGram bundles the standardized centered Gram matrices plus
// the per-predictor scale factors. Coordinate-descent and ridge solvers
// operate on this view so the regularization penalty applies uniformly
// across predictor scales; the caller de-standardizes coefficients via
// destandardizeCoeffs.
//
// All matrices are row-major p×p (or length-p vectors). M2_xx_std and
// M2_xy_std are derived from the unstandardized centered moments by
// dividing each predictor by its column standard deviation:
//
//	σ_j = √(M2_xx[j,j] / n)
//	M2_xx_std[i,j] = M2_xx[i,j] / (σ_i · σ_j)
//	M2_xy_std[j]   = M2_xy[j] / σ_j
//
// Coefficients in the standardized space recover the original-scale
// coefficients via β_j = β_std_j / σ_j; the intercept follows from
// μ_y − Σ β_j · μ_x[j].
type standardizedGram struct {
	p       int
	n       int
	m2xxStd []float64 // length p*p, symmetric, row-major
	m2xyStd []float64 // length p
	scales  []float64 // length p, σ_j = √(M2_xx[j,j] / n)
	meanX   []float64 // length p, original-scale predictor means
	meanY   float64   // original-scale target mean
	m2yy    float64   // centered Σ(y - μ_y)² — unchanged by standardization
	// degenerate flags any predictor with zero column variance after
	// centering. The solver surfaces these as
	// PROCESSING_REGRESSION_RANK_DEFICIENT before attempting Cholesky
	// or coordinate descent (a zero-scale column would divide by zero).
	degenerate bool
}

// standardizeGram builds the standardized view from the streaming
// accumulator. The accumulator's upper-triangle Gram is mirrored
// in-place by acc.finalize() before this call.
//
// scales[j] = √(M2_xx[j,j] / n). A zero scale (constant predictor)
// flags the degenerate path; coordinate descent / ridge would otherwise
// divide by zero. Callers should reject degenerate==true with
// PROCESSING_REGRESSION_RANK_DEFICIENT before consuming the result.
func standardizeGram(acc *olsAccumulator) *standardizedGram {
	p := acc.p
	n := float64(acc.n)
	out := &standardizedGram{
		p:       p,
		n:       acc.n,
		m2xxStd: make([]float64, p*p),
		m2xyStd: make([]float64, p),
		scales:  make([]float64, p),
		meanX:   append([]float64(nil), acc.meanX...),
		meanY:   acc.meanY,
		m2yy:    acc.m2YY,
	}

	const degenTol = 1e-300
	for j := 0; j < p; j++ {
		variance := acc.m2XX[j*p+j] / n
		if variance <= degenTol {
			out.scales[j] = 0
			out.degenerate = true
		} else {
			out.scales[j] = math.Sqrt(variance)
		}
	}

	if out.degenerate {
		return out
	}

	for i := 0; i < p; i++ {
		si := out.scales[i]
		for j := 0; j < p; j++ {
			sj := out.scales[j]
			out.m2xxStd[i*p+j] = acc.m2XX[i*p+j] / (si * sj)
		}
		out.m2xyStd[i] = acc.m2XY[i] / si
	}
	return out
}

// destandardizeCoeffs maps standardized coefficients back to the
// original predictor scale and recovers the intercept.
//
//	β_j      = β_std_j / σ_j
//	intercept = μ_y − Σ β_j · μ_x[j]
//
// On a degenerate gram (scale_j == 0) the corresponding β_j is set to
// zero — the caller should have already rejected the fit. Defensive
// guard here keeps the function total.
func destandardizeCoeffs(betaStd []float64, sg *standardizedGram) (coeffs []float64, intercept float64) {
	p := sg.p
	coeffs = make([]float64, p)
	intercept = sg.meanY
	for j := 0; j < p; j++ {
		if sg.scales[j] == 0 {
			coeffs[j] = 0
			continue
		}
		coeffs[j] = betaStd[j] / sg.scales[j]
		intercept -= coeffs[j] * sg.meanX[j]
	}
	return coeffs, intercept
}
