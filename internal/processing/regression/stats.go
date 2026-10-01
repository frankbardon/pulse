package regression

import "math"

// pValueForCoefficient returns the two-sided p-value of a coefficient
// estimate under the null β = 0, distributed as Student's t with df
// degrees of freedom. Mirrors processing.studentTTwoSidedP — kept in
// this subpackage because importing processing would create a cycle
// (processing → regression → processing).
//
// Degenerate inputs:
//   - df ≤ 0 (n − p − 1 < 1)        : returns NaN
//   - se == 0 with non-zero coef    : returns 0 (perfectly significant
//     by limit, but in practice this
//     signals a singular fit caught
//     upstream)
//   - se == 0 with zero coef        : returns NaN (statistic undefined)
//   - coef NaN                      : returns NaN
func pValueForCoefficient(coef, se float64, df int) float64 {
	if df <= 0 {
		return math.NaN()
	}
	if math.IsNaN(coef) {
		return math.NaN()
	}
	if se == 0 {
		if coef == 0 {
			return math.NaN()
		}
		return 0
	}
	t := coef / se
	return studentTTwoSidedP(t, float64(df))
}

// studentTTwoSidedP returns P(|T| ≥ |t|) for T ~ t(df). Uses the
// regularized incomplete beta identity I_x(df/2, 1/2) with
// x = df / (df + t²). Same derivation as processing/test_stat.go.
func studentTTwoSidedP(t, df float64) float64 {
	if df <= 0 || math.IsNaN(t) || math.IsNaN(df) {
		return math.NaN()
	}
	if math.IsInf(t, 0) {
		return 0
	}
	if t == 0 {
		return 1
	}
	x := df / (df + t*t)
	return regularizedIncompleteBeta(x, df/2.0, 0.5)
}

// regularizedIncompleteBeta returns I_x(a, b) via the Numerical Recipes
// continued-fraction expansion. Mirrors processing/test_stat.go.
func regularizedIncompleteBeta(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lga, _ := math.Lgamma(a)
	lgb, _ := math.Lgamma(b)
	lgab, _ := math.Lgamma(a + b)
	bt := math.Exp(lgab - lga - lgb + a*math.Log(x) + b*math.Log(1-x))
	if x < (a+1)/(a+b+2) {
		return bt * betacf(a, b, x) / a
	}
	return 1 - bt*betacf(b, a, 1-x)/b
}

// waldZTwoSidedP returns the two-sided Wald-z p-value 2·(1 − Φ(|β/SE|))
// under the null β = 0, where Φ is the standard normal CDF. REG_GLM
// uses this instead of the Student's t form because the binomial /
// poisson dispersion is fixed at 1 by the family assumption — there is
// no residual variance estimate that introduces extra degrees-of-
// freedom uncertainty into the test statistic. Gamma is wired but not
// numerically validated this phase; its Wald-z values are emitted on a
// dispersion=1 assumption that gamma callers should treat skeptically.
//
// Degenerate inputs:
//   - se == 0 with non-zero coef → returns 0 (perfectly significant).
//   - se == 0 with zero coef     → returns NaN (statistic undefined).
//   - coef NaN                   → returns NaN.
func waldZTwoSidedP(coef, se float64) float64 {
	if math.IsNaN(coef) {
		return math.NaN()
	}
	if se == 0 {
		if coef == 0 {
			return math.NaN()
		}
		return 0
	}
	z := coef / se
	if math.IsInf(z, 0) {
		return 0
	}
	// Two-sided: 2 · (1 − Φ(|z|)) = math.Erfc(|z|/√2).
	return math.Erfc(math.Abs(z) / math.Sqrt2)
}

// studentTQuantile returns the inverse Student-t CDF at probability p
// for df degrees of freedom — i.e., the value t* such that
// P(T ≤ t*) = p when T ~ t(df). Used by REG_BAYES_LINEAR to construct
// credible intervals: t_q = studentTQuantile(1 − (1−level)/2, 2·a_n).
//
// Implementation: a normal-distribution seed (Beasley-Springer-Moro for
// the standard normal inverse CDF) followed by 6–8 Newton-Raphson
// iterations on the Student-t CDF. The Newton step is
//
//	t_{k+1} = t_k − (CDF(t_k) − p) / PDF(t_k)
//
// where the PDF is the standard Student-t density. Convergence is
// quadratic and the seed is already within ~1% of the answer for any
// df ≥ 1 and any p in (0.01, 0.99), so the iteration count is small.
//
// Tolerance: ~1e-9 on the quantile for df ≥ 1 and p ∈ (1e-6, 1−1e-6).
// Credible intervals are not pixel-sensitive; this is overkill on
// purpose so the test for the IC width is dominated by the SE estimate,
// not by quantile rounding.
//
// Degenerate inputs:
//   - df ≤ 0 → returns NaN
//   - p ≤ 0  → returns -Inf
//   - p ≥ 1  → returns +Inf
//   - p = 0.5 → returns 0 exactly (symmetry).
func studentTQuantile(p, df float64) float64 {
	if math.IsNaN(p) || math.IsNaN(df) || df <= 0 {
		return math.NaN()
	}
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	if p == 0.5 {
		return 0
	}

	// Seed with the standard normal inverse CDF. For large df the
	// Student-t is nearly normal; for small df the seed is still close
	// enough that 6–8 Newton iterations reach the tolerance.
	t := normalInverseCDF(p)

	// Newton-Raphson on the Student-t CDF.
	// CDF(t) = 1 - 0.5 · I_x(df/2, 1/2),  x = df / (df + t²),   t > 0
	// CDF(t) =     0.5 · I_x(df/2, 1/2),                         t < 0
	// PDF(t) = Γ((df+1)/2) / (Γ(df/2) · √(πdf)) · (1 + t²/df)^(-(df+1)/2)
	logGammaHalfDfPlus1, _ := math.Lgamma((df + 1) / 2)
	logGammaHalfDf, _ := math.Lgamma(df / 2)
	logCoef := logGammaHalfDfPlus1 - logGammaHalfDf - 0.5*math.Log(math.Pi*df)

	const (
		maxIters = 32
		tol      = 1e-12
	)
	for i := 0; i < maxIters; i++ {
		cdf := studentTCDF(t, df)
		// Density at t.
		logPdf := logCoef - ((df+1)/2)*math.Log1p(t*t/df)
		pdf := math.Exp(logPdf)
		if pdf == 0 {
			break
		}
		step := (cdf - p) / pdf
		t -= step
		if math.Abs(step) < tol*(1+math.Abs(t)) {
			break
		}
	}
	return t
}

// studentTCDF returns P(T ≤ t) for T ~ t(df). Built on the regularized
// incomplete beta identity used by studentTTwoSidedP — symmetry around
// zero pins the t<0 branch.
func studentTCDF(t, df float64) float64 {
	if df <= 0 || math.IsNaN(t) || math.IsNaN(df) {
		return math.NaN()
	}
	if math.IsInf(t, 1) {
		return 1
	}
	if math.IsInf(t, -1) {
		return 0
	}
	if t == 0 {
		return 0.5
	}
	x := df / (df + t*t)
	// I_x(df/2, 1/2) is the two-sided tail mass; halve it for one tail.
	tail := 0.5 * regularizedIncompleteBeta(x, df/2.0, 0.5)
	if t > 0 {
		return 1 - tail
	}
	return tail
}

// normalInverseCDF returns the standard-normal inverse CDF Φ⁻¹(p) via
// the Beasley-Springer-Moro rational approximation (Moro 1995). Accuracy
// is ~1e-7 absolute over p ∈ (1e-10, 1−1e-10); good enough as a Newton
// seed for studentTQuantile.
func normalInverseCDF(p float64) float64 {
	if math.IsNaN(p) {
		return math.NaN()
	}
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	// Moro's coefficients.
	a := [4]float64{
		2.50662823884,
		-18.61500062529,
		41.39119773534,
		-25.44106049637,
	}
	b := [4]float64{
		-8.47351093090,
		23.08336743743,
		-21.06224101826,
		3.13082909833,
	}
	c := [9]float64{
		0.3374754822726147,
		0.9761690190917186,
		0.1607979714918209,
		0.0276438810333863,
		0.0038405729373609,
		0.0003951896511919,
		0.0000321767881768,
		0.0000002888167364,
		0.0000003960315187,
	}

	u := p - 0.5
	if math.Abs(u) < 0.42 {
		// Central region — rational approximation in u.
		r := u * u
		num := ((a[3]*r+a[2])*r+a[1])*r + a[0]
		den := (((b[3]*r+b[2])*r+b[1])*r+b[0])*r + 1.0
		return u * num / den
	}
	// Tails — Chebyshev-style series in log(-log(min(p,1−p))).
	r := p
	if u > 0 {
		r = 1 - p
	}
	r = math.Log(-math.Log(r))
	x := c[0] + r*(c[1]+r*(c[2]+r*(c[3]+r*(c[4]+r*(c[5]+r*(c[6]+r*(c[7]+r*c[8])))))))
	if u < 0 {
		x = -x
	}
	return x
}

// betacf evaluates the Lentz continued fraction for the incomplete
// beta integrand. Identical to the processing-package implementation.
func betacf(a, b, x float64) float64 {
	const (
		maxIter = 200
		eps     = 3e-15
		tiny    = 1e-300
	)
	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		m2 := 2 * m
		aa := float64(m) * (b - float64(m)) * x / ((qam + float64(m2)) * (a + float64(m2)))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		aa = -(a + float64(m)) * (qab + float64(m)) * x / ((a + float64(m2)) * (qap + float64(m2)))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			return h
		}
	}
	return h
}
