package statdist

import (
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// The chi-square distribution on the bit contract (doc.go): the
// regularized incomplete gamma P / Q behind it run on linalg/detmath's
// Log, Exp and Lgamma, every product that feeds an add or subtract is an
// explicit float64(...) barrier, so the bits are the same on every
// GOARCH (TestStatdist_ChiSquareBitsPinned,
// TestStatdist_NoFMAContraction).
//
// The algorithm (series for x < a+1, Lentz continued fraction
// otherwise, Loader's saddle-point prefactor for a ≥ 10) is checked
// against R's pchisq to a relative 1e-10 by reference_oracle_test.go and
// against gonum's distuv.ChiSquared.CDF by distuv_replacement_test.go.
// It is the only copy: internal/processing's chiSquareSurvival and
// noncentralChiSquareCDF call ChiSquareSurvival / RegularizedGammaP.

// ChiSquareCDF returns P(X² ≤ x) for a chi-square variate with df
// degrees of freedom (R's pchisq(x, df); gonum's distuv.ChiSquared{K:
// df}.CDF(x)): the regularized lower incomplete gamma P(df/2, x/2).
// 0 for x ≤ 0, 1 at +Inf, NaN for df ≤ 0 or a NaN argument.
func ChiSquareCDF(x, df float64) float64 {
	if df <= 0 || math.IsNaN(x) || math.IsNaN(df) {
		return math.NaN()
	}
	if x <= 0 {
		return 0
	}
	if math.IsInf(x, 1) {
		return 1
	}
	return RegularizedGammaP(df/2, x/2)
}

// ChiSquareSurvival returns P(X² ≥ x) for a chi-square variate with df
// degrees of freedom (R's pchisq(x, df, lower.tail = FALSE)): the
// regularized upper incomplete gamma Q(df/2, x/2), the chi-square test
// p-value. 1 for x ≤ 0, 0 at +Inf, NaN for df ≤ 0 or a NaN argument.
func ChiSquareSurvival(x, df float64) float64 {
	if df <= 0 || math.IsNaN(x) || math.IsNaN(df) {
		return math.NaN()
	}
	if x <= 0 {
		return 1
	}
	if math.IsInf(x, 1) {
		return 0
	}
	return regularizedGammaQ(df/2, x/2)
}

// regularizedGammaQ returns Q(a, x) = 1 − P(a, x): the series for
// x < a+1 (complemented), the continued fraction otherwise.
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

// RegularizedGammaP returns P(a, x), the regularized lower incomplete
// gamma, on the branch that keeps it relatively accurate when small: the
// series directly for x < a+1, 1 − Q otherwise. NaN for x < 0 or a ≤ 0.
func RegularizedGammaP(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	if x == 0 {
		return 0
	}
	if x < a+1 {
		return gammaSeries(a, x)
	}
	return 1 - gammaContinuedFraction(a, x)
}

// gammaPrefactor returns x^a e^{-x} / Γ(a). For a ≥ 10 it is assembled
// as √(a/2π) · exp(−δ(a) − bd0(a, x)) (Loader's saddle-point form, the
// one R's dpois_raw uses) so a·log x − x − lgamma(a) never cancels.
func gammaPrefactor(a, x float64) float64 {
	if a < 10 {
		lga, _ := detmath.Lgamma(a)
		return detmath.Exp(float64(a*detmath.Log(x)) - x - lga)
	}
	return math.Sqrt(a/(2*math.Pi)) * detmath.Exp(-LgammaCorrection(a)-bd0(a, x))
}

// bd0 returns a·log(a/m) + m − a without cancellation when a ≈ m
// (Loader 2000).
func bd0(a, m float64) float64 {
	if math.Abs(a-m) < float64(0.1*(a+m)) {
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
	return float64(a*detmath.Log(a/m)) + m - a
}

// gammaSeries evaluates P(a, x) via its convergent series for x < a+1.
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
		del = float64(del * (x / ap))
		sum += del
		if math.Abs(del) < float64(math.Abs(sum)*eps) {
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
		d = float64(an*d) + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := float64(d * c)
		h *= del
		if math.Abs(del-1) <= eps {
			break
		}
	}
	return h * gammaPrefactor(a, x)
}
