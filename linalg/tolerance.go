package linalg

// Epsilon is the float64 machine epsilon, 2⁻⁵², the one constant behind
// every rank decision in this package.
const Epsilon = 0x1p-52

// RankTolerance is the ONE rank tolerance Pulse uses:
//
//	max(rows, cols) · Epsilon · sigmaMax
//
// where sigmaMax is the largest singular value (for a symmetric
// positive-semidefinite matrix, its largest eigenvalue). A singular value
// at or below the tolerance counts as zero. This is the LAPACK / NumPy
// matrix_rank default, so a rank Pulse reports agrees with what an
// analyst reproduces elsewhere.
func RankTolerance(rows, cols int, sigmaMax float64) float64 {
	p := rows
	if cols > p {
		p = cols
	}
	return float64(float64(p)*Epsilon) * sigmaMax
}
