package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// singular_rule.go is the ONE singular decision of the multivariate
// stack (TODO #408). Every surface that needs a covariance-shaped
// matrix's inverse, determinant or full rank asks it — the vector tests
// (TEST_HOTELLING_T2, TEST_MANOVA, TEST_BOX_M, TEST_MARDIA,
// TEST_BARTLETT_SPHERICITY), the matrix operators (MAT_PCA's and
// MAT_FACTOR's KMO / Bartlett and MAT_FACTOR's maximum-likelihood fit,
// MAT_PARTIAL_CORRELATION, MAT_COLLINEARITY's VIF, MAT_DISTANCE's
// mahalanobis, the minres SMC start) and the fitted attributes
// (ATTR_MAHALANOBIS, ATTR_PC_SCORE, ATTR_FACTOR_SCORE) — so no two of
// them can disagree on one matrix.
//
// The rule: s is singular when a diagonal entry is zero, negative or
// non-finite, or when the smallest eigenvalue of its correlation form
// D⁻¹sD⁻¹ is at or below the one rank tolerance
// linalg.RankTolerance(p, p, λ_max) (gonum SymEigen). The correlation
// form makes the decision scale-free (a member in cents and one in
// millions decide alike). A tiny positive Cholesky pivot left by
// rounding is NOT enough to call s invertible: a near-exact linear
// dependency whose reference Cholesky still succeeds is singular here,
// which is the borderline the old "InverseSPD failed" rule missed. A
// caller that then inverts still treats an inversion failure as
// singular (a defensive fallback; under the rule it should not fire).
//
// The eigenvalues are gonum-backed: the decision is portable to within
// the tolerance's margin, never bit-for-bit, and no reported VALUE
// depends on them (values stay on the FMA-free reference kernels).

// rankDeficient reports whether the symmetric covariance-shaped s
// (p × p rows) is singular under the one rule.
func rankDeficient(s [][]float64) bool { return singularDetails(s) != nil }

// singularUnderRule is the one rule over a *linalg.Sym: nil when s is
// full rank, else a PULSE_MATRIX_SINGULAR coded error carrying the
// linalg singular diagnostics (rank, condition_number, and
// dependent_indices — see singularDetails), so singularMatrixError can
// add dependent_fields exactly as it does for an InverseSPD failure.
func singularUnderRule(s *linalg.Sym) error {
	details := singularDetails(s.ToRows())
	if details == nil {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_SINGULAR,
		"matrix is rank-deficient under the rank tolerance (its correlation form's smallest eigenvalue is at or below p·ε·λmax): a member is an exact or near-exact linear combination of others",
		details)
}

// singularDetails applies the rule to s and returns nil when s is full
// rank. Otherwise it returns the diagnostics the linalg singular errors
// carry, read off the same correlation-form eigendecomposition: "rank"
// (eigenvalues above the tolerance), "condition_number" (+Inf — the
// matrix is rank-deficient) and "dependent_indices" (the ascending axis
// indices with a component above √ε in a null-space eigenvector). A
// zero, negative or non-finite diagonal entry reports those indices as
// dependent_indices and no rank (the correlation form is undefined).
func singularDetails(s [][]float64) map[string]any {
	p := len(s)
	if p == 0 {
		return nil
	}
	sd := make([]float64, p)
	var bad []int
	for i := range sd {
		v := s[i][i]
		if !(v > 0) || math.IsInf(v, 0) {
			bad = append(bad, i)
			continue
		}
		sd[i] = math.Sqrt(v)
	}
	if len(bad) > 0 {
		return map[string]any{"condition_number": math.Inf(1), "dependent_indices": bad}
	}
	r := make([][]float64, p)
	for i := range r {
		r[i] = make([]float64, p)
		for j := range r[i] {
			r[i][j] = s[i][j] / (sd[i] * sd[j])
		}
	}
	undecomposable := map[string]any{"condition_number": math.Inf(1)}
	rs, err := linalg.NewSymFromRows(r)
	if err != nil {
		return undecomposable
	}
	eig, err := linalg.SymEigen(rs)
	if err != nil {
		return undecomposable
	}
	tol := linalg.RankTolerance(p, p, eig.Values.At(0))
	if eig.Values.At(p-1) > tol {
		return nil
	}
	rank := 0
	var null []int
	for k := 0; k < p; k++ {
		if eig.Values.At(k) > tol {
			rank++
		} else {
			null = append(null, k)
		}
	}
	dependent := make([]int, 0, p)
	for i := 0; i < p; i++ {
		for _, k := range null {
			if math.Abs(eig.Vectors.At(i, k)) > 0x1p-26 {
				dependent = append(dependent, i)
				break
			}
		}
	}
	return map[string]any{"rank": rank, "condition_number": math.Inf(1), "dependent_indices": dependent}
}
