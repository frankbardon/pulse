package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
)

// solveNNLS fits REG_OLS under RegressionSpec.Bounds: the slopes marked
// in nonneg (regbounds.Mask) are constrained β_j ≥ 0, the intercept and
// every other slope are free. It minimises the least-squares objective
// on the centred Gram — RSS(β) = M2_yy − 2βᵀM2_xy + βᵀM2_xxβ, the
// intercept profiled out as μ_y − βᵀμ_x — so it runs on the streaming
// accumulator (and its weighted moments) without the rows.
//
// The unconstrained fit is solved first (solveOLS, the singular-fit
// refusal path included). When it already satisfies every bound it is
// the constrained optimum too (the objective is strictly convex) and is
// returned untouched — bit for bit plain OLS — with no active bound.
// Otherwise Lawson–Hanson's active-set method runs on the Gram (Lawson &
// Hanson 1974, ch. 23; free variables never leave the passive set): each
// step solves the unconstrained subsystem M2_xx[P,P]·β_P = M2_xy[P] over
// the passive set P by Cholesky, and the final coefficients are that
// solve over the final P, the bound slopes exactly 0.
//
// A binding bound makes the estimator constrained, so its classical
// standard errors are not valid: every StdErrors entry (and the
// variances behind Vcov) is NaN — null on the wire — while the
// coefficients, R², adjusted R² and the residual standard error stand.
// The returned active lists the bound slope indices (ascending); empty
// means no bound binds.
//
// Errors: solveOLS's (INSUFFICIENT_DATA, RANK_DEFICIENT); a passive
// subsystem that fails to factor is RANK_DEFICIENT; an active-set loop
// that exceeds its iteration cap is NO_CONVERGE.
func solveNNLS(a *olsAccumulator, nonneg []bool) (*olsSolveResult, []int, error) {
	ols, err := solveOLS(a)
	if err != nil {
		return nil, nil, err
	}
	p := a.p
	feasible := true
	for j := 0; j < p; j++ {
		if nonneg[j] && ols.Coefficients[j] < 0 {
			feasible = false
			break
		}
	}
	if feasible {
		return ols, nil, nil
	}

	beta, err := lawsonHanson(p, a.m2XX, a.m2XY, nonneg)
	if err != nil {
		return nil, nil, err
	}
	var active []int
	for j := 0; j < p; j++ {
		if nonneg[j] && beta[j] == 0 {
			active = append(active, j)
		}
	}
	if len(active) == 0 {
		// Numerically the active-set loop ended on the unconstrained
		// optimum after all: report it as plain OLS.
		return ols, nil, nil
	}

	intercept := a.meanY
	for j := 0; j < p; j++ {
		intercept -= float64(beta[j] * a.meanX[j])
	}
	// RSS = M2_yy − 2·βᵀM2_xy + βᵀM2_xx·β (solveRidge's identity).
	rss := a.m2YY
	for j := 0; j < p; j++ {
		rss -= float64(2.0 * float64(beta[j]*a.m2XY[j]))
	}
	for i := 0; i < p; i++ {
		row := 0.0
		for j := 0; j < p; j++ {
			row += float64(a.m2XX[i*p+j] * beta[j])
		}
		rss += float64(beta[i] * row)
	}
	if rss < 0 {
		if rss > -1e-9*a.m2YY {
			rss = 0
		} else {
			return nil, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
				"residual sum of squares is negative; bounded fit numerically singular",
				map[string]any{"rss": rss, "tss": a.m2YY},
			)
		}
	}
	df := a.residualDF()
	_, sigma2 := a.residualVariances(rss)
	tss := a.m2YY
	var r2, adjR2 float64
	if tss > 0 {
		r2 = 1 - rss/tss
		adjR2 = a.adjustedR2(r2, df)
	}
	nan := make([]float64, p+1)
	variances := make([]float64, p+1)
	for j := range nan {
		nan[j] = math.NaN()
		variances[j] = math.NaN()
	}
	return &olsSolveResult{
		// slopeCov stays nil: olsVcov reports no covariance, and the
		// engine attaches an all-null one (boundedVcov).
		sigma2Gram:     math.NaN(),
		variances:      variances,
		Coefficients:   beta,
		Intercept:      intercept,
		StdErrors:      nan,
		RSS:            rss,
		TSS:            tss,
		R2:             r2,
		AdjR2:          adjR2,
		Sigma2:         sigma2,
		ResidualStdErr: sqrt(sigma2),
		DF:             df,
	}, active, nil
}

// lawsonHanson minimises ½βᵀGβ − cᵀβ over β_j ≥ 0 for every j with
// nonneg[j] (the rest free) — least squares on the Gram G (p×p,
// row-major, symmetric positive-definite) and c. Products carry explicit
// float64 conversions so the active-set decisions do not move with FMA
// fusion across architectures.
func lawsonHanson(p int, g, c []float64, nonneg []bool) ([]float64, error) {
	passive := make([]bool, p)
	for j := 0; j < p; j++ {
		passive[j] = !nonneg[j]
	}
	// The start: every bounded slope at 0, the free ones at their
	// least-squares values given that (feasible by construction).
	x, err := passiveSolve(p, g, c, passive)
	if err != nil {
		return nil, err
	}
	eps := math.Nextafter(1, 2) - 1
	// R's nnls caps the outer loop at 3·p; the free variables never
	// cycle, so the same cap holds here.
	maxIter := 3*p + 3
	// rejected marks a slope whose entry into P produced a non-positive
	// solve (rounding at the KKT boundary); it is skipped until x moves.
	rejected := make([]bool, p)
	for iter := 0; ; iter++ {
		if iter >= maxIter {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_NO_CONVERGE,
				"REG_OLS bounds: the Lawson–Hanson active-set loop did not settle; the bounded predictors are likely near-collinear",
				map[string]any{"iterations": iter, "p": p},
			)
		}
		// w = c − G·x, the negative gradient; a constrained slope at 0
		// with w_j > tol_j would lower the objective by entering P.
		best, bestW := -1, 0.0
		for j := 0; j < p; j++ {
			if passive[j] || rejected[j] {
				continue
			}
			gx, scale := 0.0, math.Abs(c[j])
			for k := 0; k < p; k++ {
				t := float64(g[j*p+k] * x[k])
				gx += t
				scale += math.Abs(t)
			}
			w := c[j] - gx
			if w > 64*eps*scale && w > bestW {
				best, bestW = j, w
			}
		}
		if best < 0 {
			return x, nil
		}
		passive[best] = true
		s, err := passiveSolve(p, g, c, passive)
		if err != nil {
			return nil, err
		}
		if !(s[best] > 0) {
			passive[best] = false
			rejected[best] = true
			continue
		}
		// Inner loop: step back to feasibility while a constrained
		// passive slope went non-positive.
		for {
			alpha, blockedBy := 1.0, -1
			for j := 0; j < p; j++ {
				if !passive[j] || !nonneg[j] || s[j] > 0 {
					continue
				}
				r := x[j] / (x[j] - s[j])
				if blockedBy < 0 || r < alpha {
					alpha, blockedBy = r, j
				}
			}
			if blockedBy < 0 {
				break
			}
			for j := 0; j < p; j++ {
				x[j] += float64(alpha * (s[j] - x[j]))
			}
			// The blocking slope reaches its bound exactly (rounding
			// aside); any other bounded slope the step drove to ≤ 0
			// leaves P with it.
			x[blockedBy] = 0
			for j := 0; j < p; j++ {
				if passive[j] && nonneg[j] && x[j] <= 0 {
					passive[j] = false
					x[j] = 0
				}
			}
			if s, err = passiveSolve(p, g, c, passive); err != nil {
				return nil, err
			}
		}
		for j := 0; j < p; j++ {
			if passive[j] {
				x[j] = s[j]
			} else {
				x[j] = 0
			}
		}
		clear(rejected)
	}
}

// passiveSolve solves G[P,P]·s_P = c[P] for the passive set and returns
// s with s_j = 0 off P (all-zero when P is empty).
func passiveSolve(p int, g, c []float64, passive []bool) ([]float64, error) {
	idx := make([]int, 0, p)
	for j := 0; j < p; j++ {
		if passive[j] {
			idx = append(idx, j)
		}
	}
	s := make([]float64, p)
	k := len(idx)
	if k == 0 {
		return s, nil
	}
	sub := make([]float64, k*k)
	rhs := make([]float64, k)
	for a, i := range idx {
		rhs[a] = c[i]
		for b, j := range idx {
			sub[a*k+b] = g[i*p+j]
		}
	}
	chol, ok := factorSPD(k, sub)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"REG_OLS bounds: a passive-set Gram is not positive-definite (linearly dependent or constant predictors)",
			map[string]any{"p": p, "passive": k},
		)
	}
	sol, err := solveSPD(chol, rhs)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_RANK_DEFICIENT,
			"REG_OLS bounds: Cholesky solve failed for a passive-set Gram",
			map[string]any{"p": p, "passive": k, "gonum_error": backendErrorText(err)},
		)
	}
	for a, i := range idx {
		s[i] = sol[a]
	}
	return s, nil
}

// boundedVcov is the covariance a binding bounds fit reports when the
// spec asks for Vcov: (p+1)×(p+1), every entry NaN (null on the wire,
// keys kept) — the constrained estimator has no classical covariance.
func boundedVcov(p int) [][]float64 {
	v := make([][]float64, p+1)
	for i := range v {
		v[i] = make([]float64, p+1)
		for j := range v[i] {
			v[i][j] = math.NaN()
		}
	}
	return v
}
