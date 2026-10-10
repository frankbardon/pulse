package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_minres.go is the internal one-factor minimum-residual (minres)
// factor solver: MAT_RELIABILITY's McDonald's ω reads its loadings and
// uniquenesses, and a later MAT_FACTOR can reuse it. It is unexported
// and reaches the wire only through its caller.
//
// The fit minimises the OFF-DIAGONAL residual sum of squares
//
//	F(λ) = Σ_{i≠j} (s_ij − λ_i·λ_j)²
//
// over the loadings λ — psych::fa(nfactors = 1, fm = "minres")'s
// objective. Uniquenesses are ψ_i = s_ii − λ_i² (psych's 1 −
// communality on a correlation matrix), so a Heywood case shows ψ ≤ 0.
//
// Algorithm — exact cyclic coordinate descent. F restricted to one
// loading λ_k is a quadratic whose minimiser is
//
//	λ_k = Σ_{j≠k} s_kj·λ_j / Σ_{j≠k} λ_j²,
//
// so a sweep updates k = 0..p−1 in order, each update using the
// loadings already updated in the sweep (Gauss–Seidel). Every update is
// an exact coordinate minimisation, so F never increases; a fixed point
// is a stationary point of F, which (when ψ stays positive) is psych's
// interior minres optimum. It is pure, FMA-free arithmetic (every
// product feeding a sum is written float64(a*b)) on top of the
// reference linalg.InverseSPD, so the fit is BIT-identical on every
// architecture — no eigensolver, unlike psych's eigen-parametrised
// L-BFGS-B, which is why it is compared to psych within
// minresOracleTol rather than bitwise.
//
// Start — the squared multiple correlations (SMC), psych::smc:
// h_k = s_kk − 1/(S⁻¹)_kk (1 − 1/(R⁻¹)_kk on a correlation matrix),
// λ_k = √h_k. A singular S (no inverse) falls back to the largest
// absolute off-diagonal correlation of each row (the classic SMC
// substitute), scaled back to s_kk; StartFallback reports it.
//
// Identification: one factor is identified only with p ≥ 3 members (p =
// 2 has one off-diagonal for two loadings). The solver still runs on p
// < 3 deterministically, but the split is arbitrary; the caller decides
// whether to refuse it.

// Minres solver settings — documented constants, never request knobs:
//
//   - minresMaxIter: the sweep cap. A fit that has not met
//     minresConvTol within it is returned as is with Converged false,
//     and its caller emits PULSE_MATRIX_NOT_CONVERGED (minresWarnings)
//     — never a silent best effort.
//   - minresConvTol: convergence when the largest absolute change of
//     any loading across one full sweep is ≤ this.
const (
	minresMaxIter = 1000
	minresConvTol = 1e-12
)

// minresFit is a one-factor minres solution over a p × p input.
type minresFit struct {
	// Loadings are the factor loadings λ, signed so Σλ > 0 (Σλ = 0
	// keeps the solver's sign).
	Loadings []float64
	// Uniquenesses are ψ_i = s_ii − λ_i²; ≤ 0 is a Heywood case.
	Uniquenesses []float64
	// Start is the starting communalities h (SMC, or the fallback).
	Start []float64
	// StartFallback reports that the input had no inverse, so Start is
	// the largest-absolute-off-diagonal substitute rather than the SMC.
	StartFallback bool
	// Iterations is the number of completed coordinate sweeps.
	Iterations int
	// MaxIterations is the sweep cap the fit ran under (minresMaxIter).
	MaxIterations int
	// Converged reports whether the fit met minresConvTol within
	// minresMaxIter sweeps.
	Converged bool
	// Heywood lists, ascending, the member indices whose uniqueness is
	// ≤ 0 (|λ| ≥ √s_ii: an impossible negative error variance). Empty
	// when the fit is proper.
	Heywood []int
}

// minresOneFactor fits one factor to s, a finite symmetric covariance
// or correlation matrix with a positive diagonal, under the documented
// minresMaxIter cap. A non-finite cell or a non-positive diagonal is
// undefined input the caller screens first (judgeable); reaching the
// solver with one is a caller invariant violation, PROCESSING_INTERNAL.
func minresOneFactor(s *linalg.Sym) (minresFit, error) {
	return minresSolve(s, minresMaxIter)
}

// minresSolve is minresOneFactor with an explicit sweep cap (tests force
// a non-converged fit through it).
func minresSolve(s *linalg.Sym, maxIter int) (minresFit, error) {
	if s == nil || !judgeable(s) {
		return minresFit{}, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"minres: input matrix must be finite with a positive diagonal")
	}
	p := s.N()
	start, fallback := minresStart(s)
	lambda := make([]float64, p)
	for k := range lambda {
		lambda[k] = math.Sqrt(start[k])
	}
	fit := minresFit{Start: start, StartFallback: fallback, MaxIterations: maxIter}
	for fit.Iterations < maxIter && !fit.Converged {
		delta := 0.0
		for k := 0; k < p; k++ {
			num, den := 0.0, 0.0
			for j := 0; j < p; j++ {
				if j == k {
					continue
				}
				num += float64(s.At(k, j) * lambda[j])
				den += float64(lambda[j] * lambda[j])
			}
			next := 0.0
			if den > 0 {
				next = num / den
			}
			delta = math.Max(delta, math.Abs(next-lambda[k]))
			lambda[k] = next
		}
		fit.Iterations++
		fit.Converged = delta <= minresConvTol
	}
	sum := 0.0
	for _, l := range lambda {
		sum += l
	}
	if sum < 0 {
		for k := range lambda {
			lambda[k] = -lambda[k]
		}
	}
	fit.Loadings = lambda
	fit.Uniquenesses = make([]float64, p)
	for k, l := range lambda {
		psi := s.At(k, k) - float64(l*l)
		fit.Uniquenesses[k] = psi
		if psi <= 0 {
			fit.Heywood = append(fit.Heywood, k)
		}
	}
	return fit, nil
}

// minresStart returns the starting communalities: the SMC
// s_kk − 1/(S⁻¹)_kk from the reference InverseSPD, clamped to
// [0, s_kk]; when s has no inverse, the largest |r_kj| (j ≠ k, r the
// diagonal-scaled correlation) times s_kk, and fallback true.
func minresStart(s *linalg.Sym) (h []float64, fallback bool) {
	p := s.N()
	h = make([]float64, p)
	if inv, err := linalg.InverseSPD(s); err == nil {
		for k := 0; k < p; k++ {
			h[k] = math.Min(s.At(k, k), math.Max(0, s.At(k, k)-1/inv.At(k, k)))
		}
		return h, false
	}
	for k := 0; k < p; k++ {
		best := 0.0
		for j := 0; j < p; j++ {
			if j != k {
				r := s.At(k, j) / math.Sqrt(float64(s.At(k, k)*s.At(j, j)))
				best = math.Max(best, math.Abs(r))
			}
		}
		h[k] = float64(math.Min(best, 1) * s.At(k, k))
	}
	return h, true
}

// minresWarnings is what a caller of minresOneFactor emits for the fit
// of matrix name over members: PULSE_MATRIX_NOT_CONVERGED when the
// sweeps hit the cap (details matrix, solver "minres_one_factor",
// iterations, max_iterations, tolerance). Nil for a converged fit. A
// Heywood case is reported on the fit (minresFit.Heywood); how it
// surfaces is the operator's call.
func minresWarnings(name string, fit minresFit) []*types.ResponseWarning {
	if fit.Converged {
		return nil
	}
	return []*types.ResponseWarning{notConvergedWarning(name, "minres_one_factor", fit.Iterations, fit.MaxIterations, minresConvTol,
		"matrix "+name+": the one-factor minres fit did not converge within "+strconv.Itoa(fit.MaxIterations)+
			" sweeps (tolerance "+strconv.FormatFloat(minresConvTol, 'g', -1, 64)+"); its loadings are the last iterate, not an optimum")}
}

// notConvergedWarning builds the PULSE_MATRIX_NOT_CONVERGED per-matrix
// warning every iterative matrix routine emits when it stops at its
// cap: details matrix, solver, iterations, max_iterations, tolerance.
func notConvergedWarning(name, solver string, iterations, maxIter int, tol float64, msg string) *types.ResponseWarning {
	return matrixWarning(errors.PULSE_MATRIX_NOT_CONVERGED, msg, map[string]any{
		"matrix":         name,
		"solver":         solver,
		"iterations":     iterations,
		"max_iterations": maxIter,
		"tolerance":      tol,
	})
}
