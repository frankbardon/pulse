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

// omegaSquared returns ω² for a one-way between-subjects ANOVA (Hays
// 1963; Olejnik & Algina 2003, Table 1):
//
//	ω² = (SS_between − df_between · MS_within) / (SS_total + MS_within)
//
// with SS_total = SS_between + SS_within. The unbiased estimate goes
// negative when F < 1; Pulse CLAMPS it at 0 (a negative ω² estimates
// "no effect", and a bounded [0, 1] measure is what readers compare).
// NaN when undefined (a non-positive or non-finite denominator).
func omegaSquared(ssBetween, ssWithin, dfBetween, msWithin float64) float64 {
	den := ssBetween + ssWithin + msWithin
	if !(den > 0) || math.IsInf(den, 0) {
		return math.NaN()
	}
	return max(0, (ssBetween-dfBetween*msWithin)/den)
}

// welchOmegaSquared returns the Welch-adjusted ω² estimate from the
// Welch F* statistic: the ω²-from-F identity (Lakens 2013, Frontiers
// in Psychology 4:863, ω² = (F − 1) / (F + (df_error + 1)/df_effect))
// rearranged with N = df_effect + df_error + 1 and evaluated at F*:
//
//	est. ω² = df_between · (F* − 1) / (df_between · (F* − 1) + N)
//
// With the classic F in place of F* this is algebraically identical to
// omegaSquared, so the two ANOVA arms report comparable numbers. Clamped
// at 0 like omegaSquared (F* < 1 yields a negative estimate). NaN when
// undefined (non-finite F*, N ≤ 0, or a non-positive denominator).
func welchOmegaSquared(fStar, dfBetween, n float64) float64 {
	if math.IsNaN(fStar) || math.IsInf(fStar, 0) || !(n > 0) {
		return math.NaN()
	}
	num := dfBetween * (fStar - 1)
	den := num + n
	if !(den > 0) {
		return math.NaN()
	}
	return max(0, num/den)
}

// partialEtaSquared returns partial η² = SS_effect / (SS_effect +
// SS_error) (Cohen 1973; Richardson 2011). For the one-way
// repeated-measures ANOVA the effect is the treatment (condition) and
// the error is the subject × condition residual, so between-subject
// variance is excluded from the denominator. NaN when undefined (a
// non-positive or non-finite denominator).
func partialEtaSquared(ssEffect, ssError float64) float64 {
	den := ssEffect + ssError
	if !(den > 0) || math.IsInf(den, 0) {
		return math.NaN()
	}
	return ssEffect / den
}

// epsilonSquared returns ε² for a Kruskal-Wallis H over n observations
// (Kelley 1935; Tomczak & Tomczak 2014, Trends in Sport Sciences 21(1)
// eq. 7):
//
//	ε² = H / ((n² − 1) / (n + 1))   ( ≡ H / (n − 1) )
//
// H is the statistic the test reports — tie-corrected — so ε² inherits
// the test's tie handling. NaN when undefined (n < 2, or a negative /
// non-finite H).
func epsilonSquared(h, n float64) float64 {
	if !(n >= 2) || !(h >= 0) || math.IsInf(h, 0) {
		return math.NaN()
	}
	return h / ((n*n - 1) / (n + 1))
}

// rankBiserialIndependent returns the signed Glass / Kerby (2014)
// rank-biserial correlation for a two-sample Mann-Whitney comparison:
//
//	r = (U_A − U_B) / (n_A · n_B)  =  2·U_A/(n_A·n_B) − 1
//
// whose magnitude is 1 − 2·U_min/(n_A·n_B). SIGN CONVENTION: U_A counts
// the (a, b) pairs where the group-A value is larger (ties count ½), so
// r > 0 means group A — the FIRST group in sorted Details.groups order —
// tends to be larger; the same direction as Details.z. NaN when n_A·n_B
// ≤ 0 or U_A is non-finite.
func rankBiserialIndependent(uA, nA, nB float64) float64 {
	prod := nA * nB
	if !(prod > 0) || math.IsNaN(uA) || math.IsInf(uA, 0) {
		return math.NaN()
	}
	return 2*uA/prod - 1
}

// rankBiserialPaired returns the matched-pairs rank-biserial correlation
// (Kerby 2014, Comprehensive Psychology 3:11.IT.3.1) for a Wilcoxon
// signed-rank test over the non-zero differences d = Field − Field2:
//
//	r = (W⁺ − W⁻) / (W⁺ + W⁻)
//
// SIGN CONVENTION: r > 0 means Field tends to exceed Field2 (positive
// differences carry more rank mass) — the same direction as Details.z.
// NaN when W⁺ + W⁻ ≤ 0 (no non-zero differences, or invalid negative
// rank sums) or the sum is non-finite.
func rankBiserialPaired(wPlus, wMinus float64) float64 {
	total := wPlus + wMinus
	if !(total > 0) || math.IsInf(total, 0) {
		return math.NaN()
	}
	return (wPlus - wMinus) / total
}
