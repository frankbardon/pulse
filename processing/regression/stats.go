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
