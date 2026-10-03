package processing

import (
	"math"

	"github.com/frankbardon/pulse/internal/statdist"
)

// Statistical helpers shared by the TEST_* operators. Keeping these
// dependency-free (no external math packages) avoids pulling in a large
// stats SDK for a handful of well-known distributions. The Student-t
// family and the regularized incomplete beta live in internal/statdist,
// shared with the regression engine.
//
// Every primitive here is checked against R to a relative 1e-10 by
// reference_oracle_test.go (goldens: testdata/reference/, regenerate
// with `make reference`).

// epsilon is the float64 machine epsilon (2⁻⁵²).
const epsilon = 0x1p-52

// chiSquareSurvival returns P(X² ≥ x) for a chi-square variate with df
// degrees of freedom: the complementary CDF used as the chi-square test
// p-value. Computed via the regularized upper incomplete gamma function
// Q(df/2, x/2).
func chiSquareSurvival(x, df float64) float64 {
	if df <= 0 || math.IsNaN(x) {
		return math.NaN()
	}
	if x <= 0 {
		return 1
	}
	return regularizedGammaQ(df/2.0, x/2.0)
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
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}

// normalTwoSidedP returns the two-sided standard normal p-value
// 2·Φ(−|z|) = erfc(|z|/√2). Every normal-approximation test routes its
// two-sided p through here: the complement form 2·(1 − Φ(|z|)) cancels
// to exactly 0 once |z| exceeds ~8.3, while erfc keeps full relative
// precision down to p ≈ 1e-308 (|z| ≈ 37.5). NaN in, NaN out.
func normalTwoSidedP(z float64) float64 {
	return math.Erfc(math.Abs(z) / math.Sqrt2)
}

// normalUpperTailP returns the one-sided upper-tail p-value
// P(Z ≥ z) = Φ(−z), tail-accurate for large positive z where 1 − Φ(z)
// cancels to 0.
func normalUpperTailP(z float64) float64 {
	return standardNormalCDF(-z)
}

// regularizedGammaQ returns Q(a, x) = 1 - P(a, x), the regularized
// upper incomplete gamma function. Routes between the series expansion
// (x < a+1) and the continued fraction (otherwise) for fastest
// convergence — Numerical Recipes style — with the prefactor
// x^a e^{-x} / Γ(a) from gammaPrefactor.
func regularizedGammaQ(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	if x == 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gammaSeries(a, x)
	}
	return gammaContinuedFraction(a, x)
}

// gammaPrefactor returns x^a e^{-x} / Γ(a). For a ≥ 10 it is assembled
// as √(a/2π) · exp(−δ(a) − bd0(a, x)) — Loader's saddle-point form, the
// one R's dpois_raw uses — so a·log x − x − lgamma(a) never cancels at
// large df.
func gammaPrefactor(a, x float64) float64 {
	if a < 10 {
		lga, _ := math.Lgamma(a)
		return math.Exp(a*math.Log(x) - x - lga)
	}
	return math.Sqrt(a/(2*math.Pi)) * math.Exp(-statdist.LgammaCorrection(a)-bd0(a, x))
}

// bd0 returns a·log(a/m) + m − a without cancellation when a ≈ m
// (Loader 2000, "Fast and accurate computation of binomial
// probabilities").
func bd0(a, m float64) float64 {
	if math.Abs(a-m) < 0.1*(a+m) {
		v := (a - m) / (a + m)
		s := (a - m) * v
		ej := 2 * a * v
		v *= v
		for j := 1; j < 1000; j++ {
			ej *= v
			s1 := s + ej/float64(2*j+1)
			if s1 == s {
				return s1
			}
			s = s1
		}
		return s
	}
	return a*math.Log(a/m) + m - a
}

// gammaSeries evaluates P(a, x) via its convergent series for x < a+1.
// The term ratio is x/(a+n), so convergence takes O(√a) terms near
// x ≈ a; the cap covers df ≈ 1e7.
func gammaSeries(a, x float64) float64 {
	const (
		maxIter = 100000
		eps     = 1e-17
	)
	ap := a
	sum := 1.0 / a
	del := sum
	for range maxIter {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*eps {
			break
		}
	}
	return sum * gammaPrefactor(a, x)
}

// gammaContinuedFraction evaluates Q(a, x) via Lentz's continued
// fraction for x ≥ a+1.
func gammaContinuedFraction(a, x float64) float64 {
	const (
		maxIter = 100000
		eps     = 1e-16
		tiny    = 1e-300
	)
	b := x + 1 - a
	c := 1.0 / tiny
	d := 1.0 / b
	h := d
	for i := 1; i <= maxIter; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) <= eps {
			break
		}
	}
	return h * gammaPrefactor(a, x)
}

// kolmogorovSurvival approximates Q_KS(λ) = 2 Σ (-1)^(j-1) exp(-2 j² λ²),
// the survival function of the Kolmogorov distribution. Used to convert
// the Kolmogorov-Smirnov D statistic into a two-sided p-value:
//
//	p = Q_KS((√en + 0.12 + 0.11/√en) * D)
//
// where en = n1*n2/(n1+n2). The first few terms of the series converge
// quickly; the loop bails once a term drops below 10⁻¹². Returns 1 for
// λ ≤ 0.
func kolmogorovSurvival(lambda float64) float64 {
	if lambda <= 0 {
		return 1
	}
	const (
		eps1    = 1e-6
		eps2    = 1e-16
		maxIter = 100
	)
	a2 := -2 * lambda * lambda
	fac := 2.0
	sum := 0.0
	termPrev := 0.0
	for j := 1; j <= maxIter; j++ {
		term := fac * math.Exp(a2*float64(j*j))
		sum += term
		if math.Abs(term) <= eps1*termPrev || math.Abs(term) <= eps2*sum {
			return sum
		}
		fac = -fac
		termPrev = math.Abs(term)
	}
	return 1
}
