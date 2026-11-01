package processing

import (
	"math"
	"sync"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// rank_cortest.go is the ONE two-sided p-value of a rank correlation —
// R cor.test(method = "kendall" / "spearman") at its defaults
// (exact = NULL, continuity = FALSE). TEST_KENDALL_TAU and
// TEST_SPEARMAN_R (row and post variants) and MAT_CORRELATION's rank
// cells (params.inference) all call kendallCorTestP / spearmanCorTestP,
// so the same pair under the same method has the same p everywhere in
// the engine.
//
//   - Kendall: no ties and n < 50 → the exact null distribution of the
//     concordant-pair count (R pkendall: q = C, p = min(1, 2·P[T ≤ q])
//     or 2·P[T ≥ q] on the upper side). Otherwise the normal
//     approximation z = (C − D)/√Var(S) with the tie-corrected variance
//     (kendallVarS), no continuity correction.
//   - Spearman: no ties and n ≤ 1290 → AS 89 on S = Σd² (pRho: the exact
//     permutation distribution for n ≤ 9, the Edgeworth series above).
//     Otherwise the t approximation t = ρ√((n − 2)/(1 − ρ²)) on n − 2
//     (correlationInference).
//
// Weights: frequency only (the callers refuse a probability weight).
// n = Σw and a row of weight above 1 is a tie group of that size, so a
// weighted sample takes the exact path only when every massed weight
// is 1 — exactly the unweighted test on the rep() expansion. A
// fractional frequency weight never arrives (it is an invalid row).

// kendallExactMaxN: cor.test's exact Kendall test runs below n = 50.
const kendallExactMaxN = 50

// spearmanExactMaxN: cor.test's AS 89 runs up to n = 1290.
const spearmanExactMaxN = 1290

// kendallCorTestP is Kendall's τ-b two-sided p from the Knight counts
// (pair masses C, D), n = Σw and the (expanded) tie-group sizes; NaN
// when τ is undefined.
func kendallCorTestP(tau, c, d, n float64, tiesX, tiesY []float64) float64 {
	if math.IsNaN(tau) || n < 2 {
		return math.NaN()
	}
	if n < kendallExactMaxN && len(tiesX) == 0 && len(tiesY) == 0 {
		nn := int(math.Round(n))
		q := math.Round(c)
		var p float64
		if q > float64(nn*(nn-1))/4 {
			p = 1 - pKendall(q-1, nn)
		} else {
			p = pKendall(q, nn)
		}
		return math.Min(2*p, 1)
	}
	varS := kendallVarS(n, tiesX, tiesY)
	if !(varS > 0) {
		return math.NaN()
	}
	return normalTwoSidedP((c - d) / math.Sqrt(varS))
}

// kendallVarS is Var(S) of Kendall's S = C − D under the null with the
// tie correction (Kendall 1948; R cor.test's var_S): n = Σw, tie-group
// sizes expanded. TEST_KENDALL_TAU and the matrix's Kendall p share it.
func kendallVarS(n float64, tiesX, tiesY []float64) float64 {
	v0 := float64(n * (n - 1) * (2*n + 5))
	vt := tieKendallSumW(tiesX)
	vu := tieKendallSumW(tiesY)
	v1 := 0.0
	if n > 1 {
		v1 = tieKendallV1W(tiesX) * tieKendallV1W(tiesY) / (2 * n * (n - 1))
	}
	v2 := 0.0
	if n > 2 {
		v2 = tieKendallV2W(tiesX) * tieKendallV2W(tiesY) / (9 * n * (n - 1) * (n - 2))
	}
	return (v0-vt-vu)/18 + v1 + v2
}

// spearmanCorTestP is Spearman's ρ two-sided p from the mid-rank
// columns (unit weights whenever the exact path runs: a weight above 1
// is a tie), n = Σw and the tie-group sizes; NaN when ρ is undefined.
func spearmanCorTestP(rho float64, rx, ry []float64, n float64, tiesX, tiesY []float64) float64 {
	if math.IsNaN(rho) || n < 2 {
		return math.NaN()
	}
	if n <= spearmanExactMaxN && len(tiesX) == 0 && len(tiesY) == 0 {
		nn := int(math.Round(n))
		// S = Σd², an exact even integer on untied ranks.
		var q float64
		for i := range rx {
			d := rx[i] - ry[i]
			q += float64(d * d)
		}
		var p float64
		if q > (float64(n*n*n)-n)/6 {
			p = pRho(q, nn, false)
		} else {
			p = pRho(q+2, nn, true)
		}
		return math.Min(2*p, 1)
	}
	return correlationInference(rho, n-2, 0, false, 0).P
}

// pKendall is P[T ≤ q] for T the concordant-pair count of n untied
// pairs under independence (R's C pkendall: the Mahonian counts summed
// in R's order over n!).
func pKendall(q float64, n int) float64 {
	q = math.Floor(q + 1e-7)
	u := n * (n - 1) / 2
	if q < 0 {
		return 0
	}
	if q > float64(u) {
		return 1
	}
	counts := kendallCounts0(n)
	p := 0.0
	for j := 0; j <= int(q); j++ {
		p += counts[j]
	}
	return p / factorial(n)
}

// factorial is n! as R's gammafn(n + 1) computes it for n + 1 ≤ 50: a
// running product.
func factorial(n int) float64 {
	v := 1.0
	for i := 2; i <= n; i++ {
		v *= float64(i)
	}
	return v
}

var (
	kendallTableMu sync.Mutex
	kendallTables  = map[int][]float64{}
)

// kendallCounts0 is ckendall(k, n) for k = 0 … n(n−1)/2 — permutations
// of n with k inversions — by R's recursion ckendall(k, n) =
// Σ_{i=0}^{n−1} ckendall(k − i, n − 1), summed in the same order.
func kendallCounts0(n int) []float64 {
	kendallTableMu.Lock()
	defer kendallTableMu.Unlock()
	if t, ok := kendallTables[n]; ok {
		return t
	}
	prev := []float64{1} // n = 1
	for m := 2; m <= n; m++ {
		u := m * (m - 1) / 2
		cur := make([]float64, u+1)
		for k := 0; k <= u; k++ {
			s := 0.0
			for i := 0; i < m; i++ {
				if k-i >= 0 && k-i < len(prev) {
					s += prev[k-i]
				}
			}
			cur[k] = s
		}
		prev = cur
	}
	if n <= 1 {
		prev = []float64{1}
	}
	kendallTables[n] = prev
	return prev
}

// rhoExactMaxN: AS 89 (as R's prho) enumerates permutations up to n = 9.
const rhoExactMaxN = 9

// Edgeworth coefficients of AS 89.
const (
	rhoC1  = 0.2274
	rhoC2  = 0.2531
	rhoC3  = 0.1745
	rhoC4  = 0.0758
	rhoC5  = 0.1033
	rhoC6  = 0.3932
	rhoC7  = 0.0879
	rhoC8  = 0.0151
	rhoC9  = 0.0072
	rhoC10 = 0.0831
	rhoC11 = 0.0131
	rhoC12 = 4.6e-4
)

// pRho is AS 89 as R's prho: P[S ≥ is] (upper) or P[S < is] (lower)
// for S = Σd² of n untied ranks under independence — the exact
// permutation count for n ≤ 9, the Edgeworth series above.
func pRho(is float64, n int, lower bool) float64 {
	pv := 1.0
	if lower {
		pv = 0
	}
	if n <= 1 {
		return math.NaN()
	}
	if is <= 0 {
		return pv
	}
	n3 := float64(n)
	n3 *= (float64(n3*n3) - 1) / 3
	if is > n3 {
		return 1 - pv
	}
	if n <= rhoExactMaxN {
		counts := rhoCounts(n)
		var ifr, nfac float64
		for s, c := range counts {
			nfac += c
			if float64(s) >= is {
				ifr += c
			}
		}
		if lower {
			return (nfac - ifr) / nfac
		}
		return ifr / nfac
	}
	b := 1 / float64(n)
	nf := float64(n)
	x := (6*(is-1)*b/(float64(nf*nf)-1) - 1) * math.Sqrt(1/b-1)
	y := x * x
	// Edgeworth series, innermost bracket first; every product feeding
	// an add is barriered (float64) so arm64 cannot fuse it.
	inner4 := rhoC11 - float64(rhoC12*y)
	inner3 := rhoC9 - float64(rhoC10*b) + float64(float64(y*b)*inner4)
	inner2 := rhoC7 + float64(rhoC8*b) - float64(y*inner3)
	inner1 := -rhoC4 + float64(b*(rhoC5+float64(rhoC6*b))) - float64(float64(y*b)*inner2)
	u := x * b * (rhoC1 + float64(b*(rhoC2+float64(rhoC3*b))) + float64(y*inner1))
	y = u / detmath.Exp(y/2)
	up := y + normalUpperTailP(x)
	up = math.Max(0, math.Min(1, up))
	if lower {
		return 1 - up
	}
	return up
}

var (
	rhoTableMu sync.Mutex
	rhoTables  = map[int][]float64{}
)

// rhoCounts is the number of permutations of 1 … n with Σ(i − π(i))² =
// s, for s = 0 … (n³ − n)/3 (n ≤ 9: at most 362,880 permutations, once
// per n per process).
func rhoCounts(n int) []float64 {
	rhoTableMu.Lock()
	defer rhoTableMu.Unlock()
	if t, ok := rhoTables[n]; ok {
		return t
	}
	counts := make([]float64, (n*n*n-n)/3+1)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	score := func() int {
		s := 0
		for i, v := range perm {
			d := i - v
			s += d * d
		}
		return s
	}
	// Heap's algorithm, iterative.
	c := make([]int, n)
	counts[score()]++
	for i := 0; i < n; {
		if c[i] < i {
			if i%2 == 0 {
				perm[0], perm[i] = perm[i], perm[0]
			} else {
				perm[c[i]], perm[i] = perm[i], perm[c[i]]
			}
			counts[score()]++
			c[i]++
			i = 0
		} else {
			c[i] = 0
			i++
		}
	}
	rhoTables[n] = counts
	return counts
}
