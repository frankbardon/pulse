package linalg

import "math"

// dependenceTolerance is the magnitude a component of a null-space
// eigenvector must exceed for its variable to be reported as part of a
// linear dependency (details "dependent_indices"). The eigenvectors are
// unit length, so components at or below √Epsilon (2⁻²⁶) are rounding
// noise, not participation.
const dependenceTolerance = 0x1p-26

// singularDiagnostics adds the PULSE_MATRIX_SINGULAR diagnostics for the
// symmetric s to details, from one gonum-backed SymEigen (|λ| are s's
// singular values):
//
//   - "rank": the eigenvalues with |λ| above RankTolerance(n, n,
//     max|λ|) — the ONE rank tolerance Rank uses;
//   - "condition_number": max|λ| / min|λ|, +Inf when rank < n (null on
//     the wire) — ConditionNumber's definition;
//   - "dependent_indices": only when rank < n — the ascending axis
//     indices with a component above dependenceTolerance in some
//     null-space eigenvector (|λ| at or below the tolerance), i.e. the
//     variables in an exact or near-exact linear dependency. A full-rank
//     indefinite matrix has no dependency, so no key.
//
// Nothing is added for a matrix carrying a NaN / ±Inf element or whose
// decomposition fails: the diagnostics are identifiable there only.
// Diagnostic only — no reference kernel's output depends on it.
func singularDiagnostics(s *Sym, details map[string]any) {
	n := s.N()
	if n == 0 || !allFinite(s.data) {
		return
	}
	eig, err := SymEigen(s)
	if err != nil {
		return
	}
	maxAbs, minAbs := 0.0, math.Inf(1)
	for i := 0; i < n; i++ {
		a := math.Abs(eig.Values.At(i))
		maxAbs, minAbs = math.Max(maxAbs, a), math.Min(minAbs, a)
	}
	tol := RankTolerance(n, n, maxAbs)
	rank := 0
	var null []int
	for k := 0; k < n; k++ {
		if math.Abs(eig.Values.At(k)) > tol {
			rank++
		} else {
			null = append(null, k)
		}
	}
	details["rank"] = rank
	if rank < n {
		details["condition_number"] = math.Inf(1)
	} else {
		details["condition_number"] = maxAbs / minAbs
	}
	if len(null) == 0 {
		return
	}
	dependent := make([]int, 0, n)
	for i := 0; i < n; i++ {
		for _, k := range null {
			if math.Abs(eig.Vectors.At(i, k)) > dependenceTolerance {
				dependent = append(dependent, i)
				break
			}
		}
	}
	details["dependent_indices"] = dependent
}
