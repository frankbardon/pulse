package mcmc

import (
	"math"
	"sort"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// The diagnostics follow Vehtari et al. (2021), "Rank-normalization,
// folding, and localization: an improved R̂ for assessing convergence of
// MCMC", step for step as ArviZ (arviz-stats 1.3.3) computes them, so the
// committed ArviZ references in testdata/diagnostics.json pin them.
//
// Every function takes one parameter's draws as chains[chain][draw]. The
// chains must be equal-length and every draw finite; anything else, or
// too few draws, returns NaN.

// constTol is the range below which ESS treats a parameter as constant
// (NumPy's float64 resolution, as ArviZ uses).
const constTol = 1e-15

// RHat returns the rank-normalised split R̂: each chain is split in half,
// the pooled halves are rank-normalised, and R̂ is computed on them (bulk)
// and on the rank-normalised |θ − median| (folded); the larger is
// returned. It needs at least two chains of at least four draws. Values
// near 1 (below 1.01) indicate the chains agree.
func RHat(chains [][]float64) float64 {
	if !validChains(chains, 2) {
		return math.NaN()
	}
	split := splitChains(chains)
	bulk := rhatOf(zScale(split))
	med := median(split)
	folded := make([][]float64, len(split))
	for i, ch := range split {
		f := make([]float64, len(ch))
		for j, x := range ch {
			f[j] = math.Abs(x - med)
		}
		folded[i] = f
	}
	tail := rhatOf(zScale(folded))
	if tail > bulk {
		return tail
	}
	return bulk
}

// ESSBulk returns the bulk effective sample size: the ESS of the
// rank-normalised split chains. It needs at least four draws per chain.
func ESSBulk(chains [][]float64) float64 {
	if !validChains(chains, 1) {
		return math.NaN()
	}
	return ess(zScale(splitChains(chains)))
}

// ESSTail returns the tail effective sample size: the smaller of the ESS
// of the split indicator chains 1{θ ≤ q₀.₀₅} and 1{θ ≤ q₀.₉₅}, the
// quantiles taken over all draws (linear interpolation, Hyndman–Fan
// type 7). It needs at least four draws per chain.
func ESSTail(chains [][]float64) float64 {
	if !validChains(chains, 1) {
		return math.NaN()
	}
	lo := ess(splitChains(indicator(chains, 0.05)))
	hi := ess(splitChains(indicator(chains, 0.95)))
	if hi < lo {
		return hi
	}
	return lo
}

func validChains(chains [][]float64, minChains int) bool {
	if len(chains) < minChains || len(chains) == 0 {
		return false
	}
	n := len(chains[0])
	if n < 4 {
		return false
	}
	for _, ch := range chains {
		if len(ch) != n {
			return false
		}
		for _, x := range ch {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return false
			}
		}
	}
	return true
}

// splitChains halves every chain into its first and last ⌊n/2⌋ draws
// (an odd chain drops its middle draw): chains 0..m−1 are the first
// halves, m..2m−1 the second.
func splitChains(chains [][]float64) [][]float64 {
	half := len(chains[0]) / 2
	n := len(chains[0])
	out := make([][]float64, 0, 2*len(chains))
	for _, ch := range chains {
		out = append(out, ch[:half])
	}
	for _, ch := range chains {
		out = append(out, ch[n-half:])
	}
	return out
}

// zScale rank-normalises the pooled draws: average ranks over ties,
// Blom's offset (r − 3/8)/(S + 1/4), then the standard-normal quantile.
func zScale(chains [][]float64) [][]float64 {
	type item struct {
		v      float64
		ch, ix int
	}
	var all []item
	for c, ch := range chains {
		for i, x := range ch {
			all = append(all, item{x, c, i})
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].v < all[b].v })
	out := make([][]float64, len(chains))
	for c, ch := range chains {
		out[c] = make([]float64, len(ch))
	}
	size := float64(len(all))
	denom := size - 0.75 + 1
	for i := 0; i < len(all); {
		j := i + 1
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		rank := float64(float64(i+1+j) / 2) // the compiler turns /2 into *0.5, which would fuse
		z := normQuantile((rank - 0.375) / denom)
		for k := i; k < j; k++ {
			out[all[k].ch][all[k].ix] = z
		}
		i = j
	}
	return out
}

// normQuantile is Φ⁻¹(p) = √2·erf⁻¹(2p − 1).
func normQuantile(p float64) float64 {
	return math.Sqrt2 * detmath.Erfinv(float64(2*p)-1)
}

// rhatOf is the classic R̂ of equal-length chains:
// sqrt((B/W + n − 1)/n) with B = n·var(chain means), W = mean(chain var).
func rhatOf(chains [][]float64) float64 {
	n := float64(len(chains[0]))
	means := make([]float64, len(chains))
	var within float64
	for c, ch := range chains {
		means[c] = mean(ch)
		within += variance(ch, means[c])
	}
	within /= float64(len(chains))
	between := float64(n * variance(means, mean(means)))
	return math.Sqrt((between/within + n - 1) / n)
}

func mean(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// variance is the ddof = 1 sample variance about m.
func variance(xs []float64, m float64) float64 {
	var s float64
	for _, x := range xs {
		d := x - m
		s += float64(d * d)
	}
	return s / float64(len(xs)-1)
}

func median(chains [][]float64) float64 {
	var all []float64
	for _, ch := range chains {
		all = append(all, ch...)
	}
	sort.Float64s(all)
	k := len(all)
	if k%2 == 1 {
		return all[k/2]
	}
	return (all[k/2-1] + all[k/2]) / 2
}

// quantile is NumPy's "linear" (type 7) quantile of the pooled draws,
// including its interpolation formula.
func quantile(chains [][]float64, p float64) float64 {
	var all []float64
	for _, ch := range chains {
		all = append(all, ch...)
	}
	sort.Float64s(all)
	h := float64(float64(len(all)-1) * p)
	lo := math.Floor(h)
	i := int(lo)
	j := min(i+1, len(all)-1)
	t := h - lo
	a, b := all[i], all[j]
	d := b - a
	if t >= 0.5 {
		return b - float64(d*(1-t))
	}
	return a + float64(d*t)
}

// indicator returns 1{θ ≤ q_p} per draw.
func indicator(chains [][]float64, p float64) [][]float64 {
	q := quantile(chains, p)
	out := make([][]float64, len(chains))
	for c, ch := range chains {
		ind := make([]float64, len(ch))
		for i, x := range ch {
			if x <= q {
				ind[i] = 1
			}
		}
		out[c] = ind
	}
	return out
}

// ess is the multi-chain effective sample size of equal-length chains
// with Geyer's initial positive sequence and initial monotone sequence
// over the direct (biased, 1/n) autocovariance.
func ess(chains [][]float64) float64 {
	m := len(chains)
	n := len(chains[0])
	total := float64(m * n)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, ch := range chains {
		for _, x := range ch {
			lo = math.Min(lo, x)
			hi = math.Max(hi, x)
		}
	}
	if hi-lo < constTol {
		return total
	}
	nf := float64(n)
	means := make([]float64, m)
	dev := make([][]float64, m)
	for c, ch := range chains {
		means[c] = mean(ch)
		d := make([]float64, n)
		for i, x := range ch {
			d[i] = x - means[c]
		}
		dev[c] = d
	}
	// acov is the chain-mean autocovariance at lag k.
	acov := func(k int) float64 {
		var s float64
		for _, d := range dev {
			var a float64
			for i := 0; i+k < n; i++ {
				a += float64(d[i] * d[i+k])
			}
			s += a / nf
		}
		return s / float64(m)
	}
	meanVar := float64(acov(0)*nf) / (nf - 1)
	varPlus := float64(meanVar*(nf-1)) / nf
	if m > 1 {
		varPlus += variance(means, mean(means))
	}
	rho := make([]float64, n)
	rhoEven := 1.0
	rho[0] = rhoEven
	rhoOdd := 1 - (meanVar-acov(1))/varPlus
	rho[1] = rhoOdd
	t := 1
	for t < n-3 && rhoEven+rhoOdd > 0 {
		rhoEven = 1 - (meanVar-acov(t+1))/varPlus
		rhoOdd = 1 - (meanVar-acov(t+2))/varPlus
		if rhoEven+rhoOdd >= 0 {
			rho[t+1] = rhoEven
			rho[t+2] = rhoOdd
		}
		t += 2
	}
	maxT := t - 2
	if rhoEven > 0 {
		rho[maxT+1] = rhoEven
	}
	for t = 1; t <= maxT-2; t += 2 {
		if rho[t+1]+rho[t+2] > rho[t-1]+rho[t] {
			rho[t+1] = (rho[t-1] + rho[t]) / 2
			rho[t+2] = rho[t+1]
		}
	}
	var head float64
	for _, r := range rho[:maxT+1] {
		head += r
	}
	tau := -1 + float64(2*head) + rho[maxT+1]
	tau = math.Max(tau, 1/(detmath.Log(total)/math.Ln10))
	for _, r := range rho {
		if math.IsNaN(r) {
			return math.NaN()
		}
	}
	return total / tau
}
