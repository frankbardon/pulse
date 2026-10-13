package processing

import (
	"math"
	"sync"

	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// matrix_rank_inference.go is params.inference on MAT_CORRELATION's rank
// methods (params.method "spearman" / "kendall"): each cell's two-sided
// p-value is R cor.test(method = …)'s default (exact = NULL,
// continuity = FALSE) over the cell's own massed rows — the frequency
// expansion under a frequency weight, where n = Σw and a weight above 1
// is a tie — and its interval is a Fisher-z interval with the method's
// own standard error, which cor.test does not report.
//
// p (rankCellP):
//
//   - Kendall: no ties and n < 50 → the exact null distribution of the
//     concordant-pair count (R pkendall: q = C, p = min(1, 2·P[T ≤ q])
//     or 2·P[T ≥ q] on the upper side). Otherwise the normal
//     approximation z = (C − D)/√Var(S) with the tie-corrected variance
//     (kendallVarS, TEST_KENDALL_TAU's), no continuity correction.
//   - Spearman: no ties and n ≤ 1290 → AS 89 on S = Σd² (pRho: the exact
//     permutation distribution for n ≤ 9, the Edgeworth series above).
//     Otherwise the t approximation t = ρ√((n − 2)/(1 − ρ²)) on n − 2.
//
// Interval: tanh(atanh(r) ∓ z_{α/2}·se), se² = (1 + ρ²/2)/(n − 3) for
// Spearman (Bonett & Wright 2000) and 0.437/(n − 4) for Kendall's τ
// (Fieller, Hartley & Pearson 1957); |r| = 1 gives [r, r]. Floors: p
// needs 3 rows and n − 2 > 0 (the shared rule); the interval 4 rows and
// n − 3 > 0 (Spearman) or 5 rows and n − 4 > 0 (Kendall). N* is the
// shared pairSize (unweighted rows, Σw under frequency; a rank method
// takes no probability weight), so the thin-cell nulls and warnings are
// inferenceAux's.

// rankInference is MAT_CORRELATION's params.inference under a rank
// method: pv holds each cell's p off rankCorrelation.
func (in *matrixFinalizeInput) rankInference(r *linalg.Sym, pv [][]float64) (map[string]*types.MatrixValues, []*types.ResponseWarning) {
	basis := weighting.BasisOf(in.slot.weight)
	rule := inferenceRule{
		p:      func(a, b int) float64 { return pv[a][b] },
		zDF:    spearmanZDF,
		ciRows: 1,
		ciNeed: "4 rows and N* − 3 > 0 for the interval (Bonett–Wright se)",
	}
	if in.Plan().Method == "kendall" {
		rule.zDF, rule.ciRows = kendallZDF, 2
		rule.ciNeed = "5 rows and N* − 4 > 0 for the interval (Fieller se)"
	}
	return in.inferenceAux(r.At, 0, func(a, b int) pairInference {
		return pairSize(in.CM, basis, a, b)
	}, in.Plan().Pairwise, nil, rule)
}

// spearmanZDF is 1/se² of atanh(ρ), Bonett & Wright (2000):
// se² = (1 + ρ²/2)/(n − 3).
func spearmanZDF(rho, n float64) float64 { return (n - 3) / (1 + rho*rho/2) }

// kendallZDF is 1/se² of atanh(τ), Fieller, Hartley & Pearson (1957):
// se² = 0.437/(n − 4).
func kendallZDF(_, n float64) float64 { return (n - 4) / 0.437 }

// kendallExactMaxN: cor.test's exact Kendall test runs below n = 50.
const kendallExactMaxN = 50

// spearmanExactMaxN: cor.test's AS 89 runs up to n = 1290.
const spearmanExactMaxN = 1290

// kendallCellP is a Kendall cell's two-sided p from its Knight counts
// (pair masses C, D), n = Σw and the (expanded) tie-group sizes; NaN
// when τ is undefined.
func kendallCellP(tau, c, d, n float64, tiesX, tiesY []float64) float64 {
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
	v0 := n * (n - 1) * (2*n + 5)
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

// spearmanCellP is a Spearman cell's two-sided p from its mid-rank
// columns (unit weights whenever the exact path runs: a weight above 1
// is a tie), n = Σw and the tie-group sizes; NaN when ρ is undefined.
func spearmanCellP(rho float64, rx, ry []float64, n float64, tiesX, tiesY []float64) float64 {
	if math.IsNaN(rho) || n < 2 {
		return math.NaN()
	}
	if n <= spearmanExactMaxN && len(tiesX) == 0 && len(tiesY) == 0 {
		nn := int(math.Round(n))
		// S = Σd², an exact even integer on untied ranks.
		var q float64
		for i := range rx {
			d := rx[i] - ry[i]
			q += d * d
		}
		var p float64
		if q > (n*n*n-n)/6 {
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
	n3 *= (n3*n3 - 1) / 3
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
	x := (6*(is-1)*b/(nf*nf-1) - 1) * math.Sqrt(1/b-1)
	y := x * x
	u := x * b * (rhoC1 + b*(rhoC2+rhoC3*b) +
		y*(-rhoC4+b*(rhoC5+rhoC6*b)-
			y*b*(rhoC7+rhoC8*b-
				y*(rhoC9-rhoC10*b+y*b*(rhoC11-rhoC12*y)))))
	y = u / math.Exp(y/2)
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
