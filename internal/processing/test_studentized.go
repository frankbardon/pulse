package processing

import "math"

// Studentized-range distribution helpers used by TEST_TUKEY_HSD.
// Implemented as numerical integration without external dependencies.
//
// Definition: Q(k, ν) is the studentized range distribution — the
// distribution of the range of k iid N(0, σ²) variables divided by an
// independent estimate of σ (a chi distribution scaled by 1/ν).
//
//   P(Q ≤ q | k, ν) = ∫₀^∞ R_CDF(q·s, k) · h(s; ν) ds
//
// where:
//   R_CDF(t, k) = ∫_{-∞}^∞ k · φ(z) · [Φ(z+t) − Φ(z)]^{k-1} dz
//                                                       (range-of-k normals CDF)
//   h(s; ν)     = density of √(χ²_ν / ν)  =  2 · (ν/2)^{ν/2} / Γ(ν/2)
//                                          · s^{ν−1} · exp(−ν s² / 2)
//
// Accuracy target: relative error < 1% for k ∈ [2..20], ν ∈ [5..∞] at
// typical Tukey-HSD operating points (q ∈ [1..10], p ∈ [0.001..0.5]).
// Truncation bound s ∈ (0, 10] is generous: for ν ≥ 5 the chi density
// is effectively zero past s = 5.

// normalRangeCDF returns P(R ≤ t | k) where R is the range of k iid
// N(0, 1) variables. Computed by quadrature over the inner integral:
//
//	∫_{-∞}^∞ k · φ(z) · [Φ(z+t) − Φ(z)]^{k-1} dz
//
// Returns 0 for t ≤ 0 and 1 for very large t (k=1 degenerate case
// short-circuits to 1).
func normalRangeCDF(t float64, k int) float64 {
	if k <= 1 {
		return 1
	}
	if t <= 0 {
		return 0
	}
	// 51-point Simpson rule on (-8, 8); the integrand vanishes outside
	// that range for any practical k since φ(z) ≤ φ(8) ≈ 5e-15.
	const lo, hi = -8.0, 8.0
	const n = 200
	h := (hi - lo) / float64(n)
	sum := rangeIntegrand(lo, t, k) + rangeIntegrand(hi, t, k)
	for i := 1; i < n; i++ {
		z := lo + float64(i)*h
		w := 4.0
		if i%2 == 0 {
			w = 2.0
		}
		sum += w * rangeIntegrand(z, t, k)
	}
	return float64(k) * sum * h / 3.0
}

func rangeIntegrand(z, t float64, k int) float64 {
	phi := math.Exp(-0.5*z*z) / math.Sqrt(2*math.Pi)
	diff := standardNormalCDF(z+t) - standardNormalCDF(z)
	if diff <= 0 {
		return 0
	}
	return phi * math.Pow(diff, float64(k-1))
}

// studentizedRangeSurvival returns P(Q > q | k, ν), the survival
// function used as the Tukey HSD p-value. df = ν.
//
// For ν → ∞ collapses to 1 − R_CDF(q, k).
func studentizedRangeSurvival(q float64, k int, df float64) float64 {
	if q <= 0 {
		return 1
	}
	if math.IsInf(df, 0) || df > 200 {
		// Chi-density h(s; ν) is sharply peaked around s ≈ 1 with width
		// ≈ 1/√(2ν) for large ν; the asymptotic R_CDF(q, k) loses < 0.1%
		// accuracy once ν is above 200. Use the closed-form short-circuit
		// to avoid numerical-integration under-sampling the peak.
		return 1 - normalRangeCDF(q, k)
	}
	// Outer integral over s = √(χ²_ν / ν). Truncate at s_max = 6 — the
	// chi density is < 1e-30 past that bound for ν ≤ 200.
	const sMax = 6.0
	const n = 400
	h := sMax / float64(n)
	lgHalfNu, _ := math.Lgamma(df / 2)
	// log of the leading constant in h(s; ν):
	//   2 · (ν/2)^{ν/2} / Γ(ν/2)
	// stored in log space to avoid overflow at large ν.
	lnLeading := math.Log(2) + (df/2)*math.Log(df/2) - lgHalfNu
	integrand := func(s float64) float64 {
		if s <= 0 {
			return 0
		}
		// log density h(s; ν).
		lnH := lnLeading + (df-1)*math.Log(s) - df*s*s/2
		if lnH < -700 {
			return 0
		}
		return math.Exp(lnH) * normalRangeCDF(q*s, k)
	}
	// Composite Simpson rule.
	sum := integrand(0) + integrand(sMax)
	for i := 1; i < n; i++ {
		s := float64(i) * h
		w := 4.0
		if i%2 == 0 {
			w = 2.0
		}
		sum += w * integrand(s)
	}
	cdf := sum * h / 3.0
	if cdf > 1 {
		cdf = 1
	}
	if cdf < 0 {
		cdf = 0
	}
	return 1 - cdf
}

// studentizedRangeInverse returns q such that P(Q > q | k, df) = alpha
// via bisection on studentizedRangeSurvival. Used for Tukey HSD CI bounds.
func studentizedRangeInverse(alpha float64, k int, df float64) float64 {
	if alpha <= 0 || alpha >= 1 {
		return math.NaN()
	}
	lo, hi := 0.0, 25.0
	for range 60 {
		mid := 0.5 * (lo + hi)
		if studentizedRangeSurvival(mid, k, df) > alpha {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo < 1e-6 {
			break
		}
	}
	return 0.5 * (lo + hi)
}
