package processing

import (
	"math"

	"github.com/frankbardon/pulse/linalg"
)

// matrix_standardize.go is VectorSpec.standardize "zscore" on a matrix
// slot. Z-scoring a member with its population SD over the slot's
// listwise rows and refolding is, in co-moment terms, a rescale of the
// MERGED state: with W = Σw (n unweighted) and s_i = √(W / M2_ii),
//
//	M2'_ij = s_i · s_j · M2_ij    mean'_i = 0
//
// so Cov(0) of the result is the members' correlation matrix. Applying
// it once, after the fixed merge tree, keeps the streamed, merged and
// buffered paths bit-equal (they share that tree) and the slot
// mergeable. Counts and weight sums (N, W, W2, NEff) are untouched: the
// rows are the same rows.
//
// Only the first k columns (the vector's members) are standardized; a
// MAT_PARTIAL_CORRELATION control outside the members keeps its scale.
// A member with zero spread (M2_ii ≤ 0, or no mass) has no SD: it is
// left as folded, so every figure that touches it stays null exactly
// as without standardize — never a division by zero.

// standardizeCoMoment returns cm with its first k columns z-scored
// (above). A pairwise state is never standardized (the resolver refuses
// it); it is returned as is.
func standardizeCoMoment(cm *linalg.CoMoment, k int) (*linalg.CoMoment, error) {
	if cm.Mode() != linalg.Listwise {
		return cm, nil
	}
	st, err := cm.ListwiseState()
	if err != nil {
		return nil, err
	}
	p := st.M2.N()
	k = min(k, p)
	w := st.W
	// diag holds the folded M2_ii (st.M2 is rescaled in place: it is
	// ListwiseState's own copy); scaled[i] says member i has a spread
	// to divide by.
	diag := make([]float64, p)
	scaled := make([]bool, p)
	for i := 0; i < p; i++ {
		diag[i] = st.M2.At(i, i)
		scaled[i] = i < k && w > 0 && diag[i] > 0 && !math.IsInf(diag[i], 0)
	}
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			mij := st.M2.At(i, j)
			var v float64
			switch {
			case scaled[i] && scaled[j] && i == j:
				v = w
			case scaled[i] && scaled[j]:
				// W·M2_ij / √(M2_ii·M2_jj): the correlation times W.
				v = float64(w*mij) / corrRoot(diag[i], diag[j])
			case scaled[i]:
				v = mij * math.Sqrt(w/diag[i])
			case scaled[j]:
				v = mij * math.Sqrt(w/diag[j])
			default:
				continue
			}
			st.M2.Set(i, j, v)
		}
	}
	for i := 0; i < k; i++ {
		if scaled[i] {
			st.Mean[i] = 0
		}
	}
	return linalg.NewCoMomentFromListwise(st)
}

// corrRoot is √(a·b), or √a·√b when the product leaves the normal
// float64 range — the CoMoment.Corr denominator, so no spread
// overflows or underflows. a, b > 0.
func corrRoot(a, b float64) float64 {
	if prod := a * b; prod >= 0x1p-1022 && !math.IsInf(prod, 1) {
		return math.Sqrt(prod)
	}
	return float64(math.Sqrt(a) * math.Sqrt(b))
}
