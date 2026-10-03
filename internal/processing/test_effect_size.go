package processing

import "math"

// effectSizeDetailsKey is the TestResult.Details key every statistical
// test nests its effect sizes under: Details["effect_size"] is a
// map[string]any keyed by the measure's snake_case name (`cohens_d`,
// `cramers_v`, `phi`, `cohens_h`, `eta_squared`, …), each value a
// float64.
const effectSizeDetailsKey = "effect_size"

// setEffectSize records one named effect size under
// details["effect_size"][name], creating the nested map on first use.
// A non-finite value (NaN, ±Inf) is DROPPED, so an effect size that is
// undefined for the input (zero variance, n = 0, a 1-wide table) is
// omitted rather than emitted as a value no JSON consumer can read. The
// nested map itself is created only when at least one value lands.
func setEffectSize(details map[string]any, name string, v float64) {
	if details == nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	es, _ := details[effectSizeDetailsKey].(map[string]any)
	if es == nil {
		es = make(map[string]any)
		details[effectSizeDetailsKey] = es
	}
	es[name] = v
}

// cramersV returns Cramér's V for a χ² statistic over an r×c table of
// n observations: √(χ² / (n · (min(r,c) − 1))). NaN when undefined
// (n ≤ 0, min(r,c) < 2, or a negative / non-finite χ²).
func cramersV(chi2 float64, n float64, rows, cols int) float64 {
	k := min(rows, cols) - 1
	if n <= 0 || k < 1 || chi2 < 0 || math.IsNaN(chi2) || math.IsInf(chi2, 0) {
		return math.NaN()
	}
	return math.Sqrt(chi2 / (n * float64(k)))
}

// phiCoefficient returns the (unsigned) φ coefficient √(χ²/n) for a 2×2
// table. NaN when undefined (n ≤ 0, or a negative / non-finite χ²).
// Callers emit it only for 2×2 tables, where it equals Cramér's V.
func phiCoefficient(chi2, n float64) float64 {
	if n <= 0 || chi2 < 0 || math.IsNaN(chi2) || math.IsInf(chi2, 0) {
		return math.NaN()
	}
	return math.Sqrt(chi2 / n)
}

// cohensH returns Cohen's h for two proportions: 2·asin(√p1) −
// 2·asin(√p2) (Cohen 1988, §6.2). Signed in the p1 − p2 direction. NaN
// when either proportion lies outside [0, 1].
func cohensH(p1, p2 float64) float64 {
	if !(p1 >= 0 && p1 <= 1 && p2 >= 0 && p2 <= 1) {
		return math.NaN()
	}
	return 2*math.Asin(math.Sqrt(p1)) - 2*math.Asin(math.Sqrt(p2))
}

// cohensDOneSample returns the one-sample Cohen's d: (mean − mu) / sd,
// with sd the sample (n − 1) standard deviation. NaN when sd is zero,
// negative or non-finite.
func cohensDOneSample(mean, mu, sd float64) float64 {
	if !(sd > 0) || math.IsInf(sd, 0) {
		return math.NaN()
	}
	return (mean - mu) / sd
}
