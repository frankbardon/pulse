package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_psd.go is the shared PSD guard every DECOMPOSITION matrix
// operator (vectors.Matrix.Decomposition: partial correlation today;
// PCA, collinearity and reliability ω reuse it) runs on its input
// matrix, and the Higham nearest-correlation repair behind
// params.repair "nearest".
//
//   - A non-decomposition operator (MAT_COVARIANCE, MAT_CORRELATION)
//     keeps the detection-only PULSE_MATRIX_NOT_PSD WARNING
//     (matrix_warnings.go).
//   - A decomposition operator whose input is not PSD (only possible
//     where vectors.Matrix.PSDRisk holds — a pairwise input; a listwise
//     one is a Gram matrix) is REFUSED with a fatal PULSE_MATRIX_NOT_PSD
//     unless params.repair is "nearest", which replaces the input by
//     its nearest correlation matrix (scaled back to the input's
//     diagonal) and emits the PULSE_MATRIX_NOT_PSD warning carrying the
//     Frobenius adjustment.
//
// The judgement is notPSD's (reference Cholesky on the diagonal-scaled
// matrix with psdTolerance — FMA-free, arch-independent); the repair is
// gonum-backed (linalg.SymEigen), so its values are same-machine
// stable, portable within the eigen tolerance only.

// Nearest-correlation repair settings — Matrix::nearPD's defaults, so
// the repair reproduces nearPD(x, corr = TRUE):
//
//   - nearestMaxIter: the iteration cap (maxit);
//   - nearestConvTol: convergence when ‖Y − X‖∞ / ‖Y‖∞ ≤ this between
//     consecutive iterates (conv.tol, conv.norm.type "I");
//   - nearestEigTol: eigenvalues ≤ this × the largest are zeroed in each
//     PSD projection (eig.tol);
//   - nearestPosdTol: the final step lifts every eigenvalue below this ×
//     the largest to that floor and restores the unit diagonal
//     (posd.tol, nearPD's do2eigen), so the repaired matrix is strictly
//     positive definite and a decomposition can factor it.
const (
	nearestMaxIter = 100
	nearestConvTol = 1e-7
	nearestEigTol  = 1e-6
	nearestPosdTol = 1e-8
)

// nearestResult is a nearest-correlation repair.
type nearestResult struct {
	// X is the repaired correlation matrix (unit diagonal).
	X *linalg.Sym
	// Iterations is the number of alternating-projection iterations.
	Iterations int
	// Converged reports whether the projections met nearestConvTol
	// within nearestMaxIter (nearPD's converged). A non-converged
	// repair still returns its last iterate after the final
	// eigenvalue floor, so X is positive definite either way.
	Converged bool
	// Adjustment is ‖X − input‖_F, the Frobenius norm of the change on
	// the correlation scale (nearPD's normF).
	Adjustment float64
}

// highamNearest is the bare alternating-projections fixed point with
// Dykstra's correction (Higham 2002; Matrix::nearPD with corr = TRUE,
// do2eigen = FALSE): X alternates between the PSD cone (eigenvalues ≤
// nearestEigTol × the largest dropped) and the unit-diagonal set, the
// Dykstra increment ΔS carried across PSD projections. The result has a
// unit diagonal and is PSD only within the convergence tolerance.
// c must be a finite symmetric matrix (a correlation matrix).
func highamNearest(c *linalg.Sym) (x *linalg.Sym, iterations int, converged bool, err error) {
	n := c.N()
	x = cloneSym(c)
	ds, _ := linalg.NewSym(n, nil)
	for iterations < nearestMaxIter && !converged {
		y := x
		r := subSym(y, ds)
		eig, err := linalg.SymEigen(r)
		if err != nil {
			return nil, iterations, false, err
		}
		x = eigenRebuild(eig, func(d, top float64) (float64, bool) {
			return d, d > nearestEigTol*top
		})
		if x == nil {
			return nil, iterations, false, errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_NOT_PSD,
				"nearest correlation repair: the matrix has no positive eigenvalue",
				map[string]any{"repair": vectors.RepairNearest})
		}
		ds = subSym(x, r)
		for i := 0; i < n; i++ {
			x.Set(i, i, 1)
		}
		iterations++
		converged = normInf(subSym(y, x))/normInf(y) <= nearestConvTol
	}
	return x, iterations, converged, nil
}

// nearestCorrelation repairs the correlation matrix c to the nearest
// correlation matrix: highamNearest, then nearPD's do2eigen step —
// eigenvalues below nearestPosdTol × the largest are lifted to that
// floor and the unit diagonal restored by a symmetric rescale — so the
// result is strictly positive definite. It matches Matrix::nearPD(c,
// corr = TRUE) (TestNearestCorrelation_MatchesNearPD); the bare fixed
// point matches do2eigen = FALSE.
func nearestCorrelation(c *linalg.Sym) (nearestResult, error) {
	x, iters, conv, err := highamNearest(c)
	if err != nil {
		return nearestResult{}, err
	}
	n := x.N()
	eig, err := linalg.SymEigen(x)
	if err != nil {
		return nearestResult{}, err
	}
	if n > 0 {
		top := math.Abs(eig.Values.At(0))
		floor := nearestPosdTol * top
		if eig.Values.At(n-1) < floor {
			lifted := eigenRebuild(eig, func(d, _ float64) (float64, bool) {
				return math.Max(d, floor), true
			})
			scale := make([]float64, n)
			for i := 0; i < n; i++ {
				scale[i] = math.Sqrt(math.Max(floor, x.At(i, i)) / lifted.At(i, i))
			}
			for i := 0; i < n; i++ {
				for j := i; j < n; j++ {
					lifted.Set(i, j, float64(scale[i]*lifted.At(i, j))*scale[j])
				}
			}
			x = lifted
		}
		for i := 0; i < n; i++ {
			x.Set(i, i, 1)
		}
	}
	return nearestResult{X: x, Iterations: iters, Converged: conv, Adjustment: normFrobenius(subSym(x, c))}, nil
}

// guardPSD is the shared input guard of a decomposition operator: s is
// the matrix it is about to factor (a covariance or a correlation over
// in.Members()); corr is s's correlation as the operator defines it —
// for a pairwise covariance the pairwise correlation
// (linalg.CoMoment.Corr: each pair over its own variances, R's
// cor(use = "pairwise")), nil to derive it by scaling s by its own
// diagonal (a correlation input is its own). It returns the matrix to
// decompose — s itself, or its repair — and the warning to emit (nil
// when none):
//
//   - not judged (s returned as is): the plan carries no PSD risk
//     (vectors.Matrix.PSDRisk — listwise, or too few columns), or s has
//     a non-finite cell or a non-positive diagonal (the operator's own
//     undefined-figure rule applies first);
//   - PSD (notPSD, the warning check's rule): s as is;
//   - not PSD, no params.repair: FATAL PULSE_MATRIX_NOT_PSD (details
//     matrix, pivot, member, checked, tolerance, repair_options);
//   - not PSD, params.repair "nearest": corr repaired
//     (nearestCorrelation) and scaled back to s's own diagonal,
//     D·nearest·D with D = √diag(s) (a covariance input keeps its
//     variances; Matrix::nearPD's repaired_covariance), with a
//     PULSE_MATRIX_NOT_PSD warning adding repair, frobenius_adjustment
//     (correlation scale), iterations and converged.
func (in *matrixFinalizeInput) guardPSD(s, corr *linalg.Sym) (*linalg.Sym, *types.ResponseWarning, error) {
	plan := in.Plan()
	if !plan.PSDRisk() || !judgeable(s) || (corr != nil && !judgeable(corr)) {
		return s, nil, nil
	}
	pivot, checked, bad := notPSD(s)
	if !bad {
		return s, nil, nil
	}
	members := in.Members()
	details := map[string]any{
		"matrix":    plan.Name,
		"pivot":     pivot,
		"member":    members[pivot],
		"checked":   checked,
		"tolerance": psdTolerance,
	}
	if plan.Repair != vectors.RepairNearest {
		details["repair_options"] = []string{vectors.RepairNearest}
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_NOT_PSD,
			"matrix "+plan.Name+": the "+string(plan.Type)+" input matrix is not positive semidefinite (reference Cholesky fails at member "+members[pivot]+"), so it cannot be decomposed; set params.repair \"nearest\" to repair it",
			details)
	}
	n := s.N()
	root := make([]float64, n)
	for i := range root {
		root[i] = math.Sqrt(s.At(i, i))
	}
	c := corr
	if c == nil {
		c, _ = linalg.NewSym(n, nil)
		for i := 0; i < n; i++ {
			c.Set(i, i, 1)
			for j := i + 1; j < n; j++ {
				c.Set(i, j, s.At(i, j)/float64(root[i]*root[j]))
			}
		}
	}
	rep, err := nearestCorrelation(c)
	if err != nil {
		return nil, nil, err
	}
	out, _ := linalg.NewSym(n, nil)
	for i := 0; i < n; i++ {
		out.Set(i, i, s.At(i, i))
		for j := i + 1; j < n; j++ {
			out.Set(i, j, float64(root[i]*rep.X.At(i, j))*root[j])
		}
	}
	details["repair"] = vectors.RepairNearest
	details["frobenius_adjustment"] = rep.Adjustment
	details["iterations"] = rep.Iterations
	details["converged"] = rep.Converged
	msg := "matrix " + plan.Name + ": the input matrix was not positive semidefinite and was replaced by its nearest correlation matrix (params.repair \"nearest\"; Frobenius adjustment " +
		strconv.FormatFloat(rep.Adjustment, 'g', 6, 64) + " on the correlation scale)"
	if !rep.Converged {
		msg += "; the projection did not converge within " + strconv.Itoa(nearestMaxIter) + " iterations, so the repair is positive definite but may not be the nearest"
	}
	return out, matrixWarning(errors.PULSE_MATRIX_NOT_PSD, msg, details), nil
}

// judgeable reports whether s can be judged and repaired: every cell
// finite and every diagonal positive.
func judgeable(s *linalg.Sym) bool {
	n := s.N()
	for i := 0; i < n; i++ {
		if d := s.At(i, i); !(d > 0) || math.IsInf(d, 0) {
			return false
		}
		for j := i + 1; j < n; j++ {
			if v := s.At(i, j); math.IsNaN(v) || math.IsInf(v, 0) {
				return false
			}
		}
	}
	return true
}

// eigenRebuild returns Σ_k f(λ_k)·q_k·q_kᵀ over the eigenpairs eig for
// which f keeps λ_k (f's second result); top is the largest eigenvalue.
// Nil when f keeps none.
func eigenRebuild(eig *linalg.SymEigenResult, f func(d, top float64) (float64, bool)) *linalg.Sym {
	n := eig.Values.Len()
	var keep []int
	var vals []float64
	top := 0.0
	if n > 0 {
		top = eig.Values.At(0)
	}
	for k := 0; k < n; k++ {
		if v, ok := f(eig.Values.At(k), top); ok {
			keep = append(keep, k)
			vals = append(vals, v)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	out, _ := linalg.NewSym(n, nil)
	q := eig.Vectors
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			sum := 0.0
			for a, k := range keep {
				sum += float64(float64(q.At(i, k)*vals[a]) * q.At(j, k))
			}
			out.Set(i, j, sum)
		}
	}
	return out
}

// cloneSym returns a copy of s.
func cloneSym(s *linalg.Sym) *linalg.Sym {
	n := s.N()
	out, _ := linalg.NewSym(n, nil)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			out.Set(i, j, s.At(i, j))
		}
	}
	return out
}

// subSym returns a − b.
func subSym(a, b *linalg.Sym) *linalg.Sym {
	n := a.N()
	out, _ := linalg.NewSym(n, nil)
	for i := 0; i < n; i++ {
		for j := i; j < n; j++ {
			out.Set(i, j, a.At(i, j)-b.At(i, j))
		}
	}
	return out
}

// normInf is the infinity norm (largest absolute row sum).
func normInf(s *linalg.Sym) float64 {
	n := s.N()
	best := 0.0
	for i := 0; i < n; i++ {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum += math.Abs(s.At(i, j))
		}
		best = math.Max(best, sum)
	}
	return best
}

// normFrobenius is the Frobenius norm.
func normFrobenius(s *linalg.Sym) float64 {
	n := s.N()
	sum := 0.0
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			v := s.At(i, j)
			sum += float64(v * v)
		}
	}
	return math.Sqrt(sum)
}
