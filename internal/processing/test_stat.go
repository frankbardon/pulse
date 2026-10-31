package processing

import (
	"math"

	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// Statistical helpers shared by the TEST_* operators. Keeping these
// dependency-free (no external math packages) avoids pulling in a large
// stats SDK for a handful of well-known distributions. The Student-t
// family, the regularized incomplete beta and the chi-square / incomplete
// gamma live in internal/statdist, shared with the regression engine.
// The normal tails run on detmath.Erfc and every product feeding an add
// is a float64(...) barrier, so the figures are the same on every GOARCH.
//
// Every primitive here is checked against R to a relative 1e-10 by
// reference_oracle_test.go (goldens: testdata/reference/, regenerate
// with `make reference`).

// epsilon is the float64 machine epsilon (2⁻⁵²).
const epsilon = 0x1p-52

// chiSquareSurvival returns P(X² ≥ x) for a chi-square variate with df
// degrees of freedom: the complementary CDF used as the chi-square test
// p-value, the regularized upper incomplete gamma Q(df/2, x/2). It is
// statdist.ChiSquareSurvival, the one bit-contract implementation
// (detmath Log / Exp / Lgamma, barriered products): 1 for x ≤ 0, 0 at
// +Inf, NaN for df ≤ 0 or a NaN argument.
func chiSquareSurvival(x, df float64) float64 {
	return statdist.ChiSquareSurvival(x, df)
}

// fSurvival returns P(F ≥ f) for an F variate with (df1, df2) degrees
// of freedom: 1 - F_CDF(f) used as the ANOVA / F-test p-value. Computed
// via the regularized incomplete beta:
//
//	1 - F_CDF(f) = I_{df2/(df2 + df1*f)}(df2/2, df1/2)
func fSurvival(f, df1, df2 float64) float64 {
	if df1 <= 0 || df2 <= 0 || math.IsNaN(f) {
		return math.NaN()
	}
	if f <= 0 {
		return 1
	}
	// x = df2/(df2 + df1·f) and its complement y = df1·f/(df2 + df1·f),
	// each formed directly so neither tail loses precision.
	var x, y float64
	if df1*f > df2 {
		r := df2 / (df1 * f)
		x, y = r/(1+r), 1/(1+r)
	} else {
		r := (df1 * f) / df2
		x, y = 1/(1+r), r/(1+r)
	}
	return statdist.RegularizedIncompleteBetaXY(df2/2.0, df1/2.0, x, y)
}

// standardNormalCDF returns Φ(z), the standard normal cumulative
// distribution function, via Φ(z) = ½ erfc(−z/√2). The erfc form keeps
// full relative precision in the lower tail (down to z ≈ −37.5), where
// ½ (1 + erf(z/√2)) cancels to zero.
func standardNormalCDF(z float64) float64 {
	return 0.5 * detmath.Erfc(-z/math.Sqrt2)
}

// normalTwoSidedP returns the two-sided standard normal p-value
// 2·Φ(−|z|) = erfc(|z|/√2). Every normal-approximation test routes its
// two-sided p through here: the complement form 2·(1 − Φ(|z|)) cancels
// to exactly 0 once |z| exceeds ~8.3, while erfc keeps full relative
// precision down to p ≈ 1e-308 (|z| ≈ 37.5). NaN in, NaN out.
func normalTwoSidedP(z float64) float64 {
	return detmath.Erfc(math.Abs(z) / math.Sqrt2)
}

// normalUpperTailP returns the one-sided upper-tail p-value
// P(Z ≥ z) = Φ(−z), tail-accurate for large positive z where 1 − Φ(z)
// cancels to 0.
func normalUpperTailP(z float64) float64 {
	return standardNormalCDF(-z)
}

// noncentralChiSquareCDF returns P(X ≤ x) for a noncentral chi-square
// variate with df degrees of freedom and noncentrality ncp (R's
// pchisq(x, df, ncp)): the Poisson(ncp/2) mixture Σ_j w_j·P(df/2 + j,
// x/2) of central lower tails. The sum starts at the Poisson mode and
// walks outward in both directions, so the largest weights are added
// first and neither walk underflows; each walk stops once a weight
// falls below 1e-17 of the running sum (every P term is at most 1, and
// past the mode the weights shrink geometrically). ncp = 0 is the
// central CDF. NaN for df ≤ 0, ncp < 0 or a NaN argument.
func noncentralChiSquareCDF(x, df, ncp float64) float64 {
	if df <= 0 || ncp < 0 || math.IsNaN(x) || math.IsNaN(df) || math.IsNaN(ncp) {
		return math.NaN()
	}
	if x <= 0 {
		return 0
	}
	if math.IsInf(x, 1) {
		return 1
	}
	// Halving is exact, but the compiler rewrites it to ·0.5 and would
	// fuse it into a+j / … − mu, hence the barriers.
	a, h := float64(df/2), x/2
	if ncp == 0 {
		return statdist.RegularizedGammaP(a, h)
	}
	mu := float64(ncp / 2)
	logMu := detmath.Log(mu)
	weight := func(j float64) float64 {
		lg, _ := detmath.Lgamma(j + 1)
		return detmath.Exp(float64(j*logMu) - mu - lg)
	}
	const (
		stop    = 1e-17
		maxTerm = 1 << 20
	)
	mode := math.Floor(mu)
	sum := 0.0
	for j, n := mode, 0; n < maxTerm; j, n = j+1, n+1 {
		w := weight(j)
		sum += float64(w * statdist.RegularizedGammaP(a+j, h))
		if j > mode && w < float64(stop*math.Max(sum, math.SmallestNonzeroFloat64)) {
			break
		}
	}
	for j := mode - 1; j >= 0; j-- {
		w := weight(j)
		sum += float64(w * statdist.RegularizedGammaP(a+j, h))
		if w < float64(stop*sum) {
			break
		}
	}
	return math.Min(sum, 1)
}
