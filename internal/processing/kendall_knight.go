package processing

import (
	"math"
	"sort"
)

// kendall_knight.go is the shared Kendall τ-b kernel: Knight's
// O(n log n) concordance count (Knight 1966, "A computer method for
// calculating Kendall's tau with ungrouped data", JASA 61), frequency-
// weighted. TEST_KENDALL_TAU and MAT_CORRELATION params.method
// "kendall" both read it, so a matrix cell and the test's τ over the
// same rows are one computation.

// kendallCounts returns the pair masses Kendall's τ-b reads over the
// paired samples xs / ys, where row i stands for ws[i] identical rows
// (nil ws: every row weighs 1). Each row pair (i, j), i < j, carries
// mass ws[i]·ws[j] and lands in exactly one class:
//
//	c  — concordant: (x_i−x_j)·(y_i−y_j) > 0
//	d  — discordant: (x_i−x_j)·(y_i−y_j) < 0
//	tx — tied in x only
//	ty — tied in y only
//
// A pair tied in both counts nowhere, and copies of one row (the w
// expanded rows of a weighted row) tie in both and drop out — exactly
// the counts of the unweighted τ-b on the physically expanded sample.
//
// Algorithm: sort the rows by (x, y); the total pair mass P, the
// x-tied mass Tx', the (x, y)-tied mass Txy and — after a merge sort of
// the y sequence that counts its weighted strict inversions (the
// discordant mass d) — the y-tied mass Ty' come from runs of equal keys;
// then tx = Tx' − Txy, ty = Ty' − Txy and c = P − d − Tx' − Ty' + Txy.
// Every mass is a sum of products of the weights, so on integer weights
// (unweighted, or frequency) every figure is an exact integer while it
// stays below 2⁵³ — the same integers the pairwise loop counts.
// Callers pass finite values and non-negative weights.
func kendallCounts(xs, ys, ws []float64) (c, d, tx, ty float64) {
	n := len(xs)
	if n < 2 {
		return 0, 0, 0, 0
	}
	weight := func(i int) float64 {
		if ws == nil {
			return 1
		}
		return ws[i]
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		xa, xb := xs[idx[a]], xs[idx[b]]
		if xa != xb {
			return xa < xb
		}
		return ys[idx[a]] < ys[idx[b]]
	})
	y := make([]float64, n)
	w := make([]float64, n)
	for k, i := range idx {
		y[k], w[k] = ys[i], weight(i)
	}

	// P, Tx' and Txy from the (x, y)-sorted order: a run's internal
	// pair mass is Σ_k w_k·(Σ of the run's weights before k).
	var total, txAll, txy float64
	var cum, runX, runXY float64
	for k := 0; k < n; k++ {
		i := idx[k]
		if k > 0 && xs[i] == xs[idx[k-1]] {
			if ys[i] == ys[idx[k-1]] {
				txy += w[k] * runXY
				runXY += w[k]
			} else {
				runXY = w[k]
			}
			txAll += w[k] * runX
			runX += w[k]
		} else {
			runX, runXY = w[k], w[k]
		}
		total += w[k] * cum
		cum += w[k]
	}

	// d: weighted strict inversions of y (an x-tied run is y-ascending,
	// so it adds none), sorting y as it goes.
	d = mergeCountInversions(y, w, make([]float64, n), make([]float64, n))

	// Ty' from the y-sorted order.
	var tyAll, runY float64
	for k := 0; k < n; k++ {
		if k > 0 && y[k] == y[k-1] {
			tyAll += w[k] * runY
			runY += w[k]
		} else {
			runY = w[k]
		}
	}
	tx = txAll - txy
	ty = tyAll - txy
	c = total - d - txAll - tyAll + txy
	return c, d, tx, ty
}

// mergeCountInversions sorts y ascending in place (stable, w carried
// along) and returns Σ w_i·w_j over the pairs i < j with y_i > y_j.
// by / bw are scratch of len(y).
func mergeCountInversions(y, w, by, bw []float64) float64 {
	n := len(y)
	if n < 2 {
		return 0
	}
	mid := n / 2
	inv := mergeCountInversions(y[:mid], w[:mid], by[:mid], bw[:mid]) +
		mergeCountInversions(y[mid:], w[mid:], by[mid:], bw[mid:])
	// leftW is the weight of the left elements not yet emitted.
	var leftW float64
	for k := 0; k < mid; k++ {
		leftW += w[k]
	}
	i, j, k := 0, mid, 0
	for i < mid && j < n {
		if y[i] <= y[j] {
			by[k], bw[k] = y[i], w[i]
			leftW -= w[i]
			i++
		} else {
			// y[j] is strictly below every remaining left element.
			inv += w[j] * leftW
			by[k], bw[k] = y[j], w[j]
			j++
		}
		k++
	}
	for ; i < mid; i++ {
		by[k], bw[k] = y[i], w[i]
		k++
	}
	for ; j < n; j++ {
		by[k], bw[k] = y[j], w[j]
		k++
	}
	copy(y, by[:n])
	copy(w, bw[:n])
	return inv
}

// kendallTauB is τ-b = (c − d) / √((c + d + tx)·(c + d + ty)) over the
// pair masses kendallCounts returns; ok is false (τ undefined) when the
// denominator is 0 — a column with every pair tied.
func kendallTauB(c, d, tx, ty float64) (tau float64, ok bool) {
	denom := math.Sqrt(((c + d) + tx) * ((c + d) + ty))
	if denom == 0 {
		return math.NaN(), false
	}
	return (c - d) / denom, true
}
