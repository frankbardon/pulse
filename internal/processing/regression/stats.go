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
// x = df / (df + t²), forming x and y = t² / (df + t²) independently.
// Same derivation as internal/processing/test_stat.go; both copies are
// checked against R to a relative 1e-10 (reference_oracle_test.go).
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
	x, y, lx, ly := studentTBetaArgs(t, df)
	return regularizedIncompleteBetaLog(df/2.0, 0.5, x, y, lx, ly)
}

// studentTBetaArgs returns x = df/(df+t²), y = t²/(df+t²) and their
// logs, without forming either as one minus the other and without
// overflowing t². The logs stay finite even when x underflows (|t| past
// ~1e154), which keeps the far tail of qt-style inversions reachable.
func studentTBetaArgs(t, df float64) (x, y, lx, ly float64) {
	at := math.Abs(t)
	if at > math.Sqrt(df) {
		r := (df / at) / at // df / t², may underflow
		lr := math.Log(df) - 2*math.Log(at)
		l1 := math.Log1p(r)
		return r / (1 + r), 1 / (1 + r), lr - l1, -l1
	}
	r := (at / df) * at // t² / df, may underflow
	lr := 2*math.Log(at) - math.Log(df)
	l1 := math.Log1p(r)
	return 1 / (1 + r), r / (1 + r), -l1, lr - l1
}

// regularizedIncompleteBetaLog returns I_x(a, b) given x, y = 1 - x
// (formed independently) and their logs lx, ly, so x (or y) may have
// underflowed to 0 while the tail it implies is still representable.
// Numerical Recipes continued fraction with a Stirling-corrected
// log-space prefactor; mirrors internal/processing/test_stat.go.
func regularizedIncompleteBetaLog(a, b, x, y, lx, ly float64) float64 {
	if math.IsInf(lx, -1) {
		return 0
	}
	if math.IsInf(ly, -1) {
		return 1
	}
	bt := math.Exp(a*lx + b*ly - logBeta(a, b))
	if x < (a+1)/(a+b+2) {
		return bt * betacf(a, b, x) / a
	}
	return 1 - bt*betacf(b, a, y)/b
}

// logBeta returns log B(a, b) for a, b > 0 without lgamma cancellation
// at large arguments (R's lbeta() algorithm). Mirrors
// internal/processing/test_stat.go.
func logBeta(a, b float64) float64 {
	p, q := math.Min(a, b), math.Max(a, b)
	const lnSqrt2Pi = 0.918938533204672741780329736406 // log(sqrt(2π))
	switch {
	case p >= 10:
		corr := lgammaCorrection(p) + lgammaCorrection(q) - lgammaCorrection(p+q)
		return -0.5*math.Log(q) + lnSqrt2Pi + corr +
			(p-0.5)*math.Log(p/(p+q)) + q*math.Log1p(-p/(p+q))
	case q >= 10:
		corr := lgammaCorrection(q) - lgammaCorrection(p+q)
		lgp, _ := math.Lgamma(p)
		return lgp + corr + p - p*math.Log(p+q) + (q-0.5)*math.Log1p(-p/(p+q))
	default:
		lgp, _ := math.Lgamma(p)
		lgq, _ := math.Lgamma(q)
		lgpq, _ := math.Lgamma(p + q)
		return lgp + lgq - lgpq
	}
}

// lgammaCorrection returns the Stirling remainder
// lgamma(x) − ((x − ½)·log x − x + log √(2π)) for x ≥ 10.
func lgammaCorrection(x float64) float64 {
	coef := [...]float64{
		1.0 / 12, -1.0 / 360, 1.0 / 1260, -1.0 / 1680,
		1.0 / 1188, -691.0 / 360360, 1.0 / 156, -3617.0 / 122400,
	}
	inv := 1 / x
	inv2 := inv * inv
	sum := 0.0
	for i := len(coef) - 1; i >= 0; i-- {
		sum = sum*inv2 + coef[i]
	}
	return sum * inv
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
// Implementation: by symmetry t* = ∓ the two-sided critical value at
// alpha = 2·min(p, 1−p) (1−p is exact for p ≥ ½), found by a bracketed
// Newton iteration on log P(|T| ≥ t) with bisection fallback. The root
// is as accurate as the tail probability itself — relative 1e-10 or
// better against R's qt() for p down to 1e-300 and df ∈ [1, 1e5]
// (reference_oracle_test.go).
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
	if p < 0.5 {
		return -studentTCriticalTwoSided(2*p, df)
	}
	return studentTCriticalTwoSided(2*(1-p), df)
}

// studentTCriticalTwoSided returns q > 0 with P(|T| ≥ q) = alpha.
func studentTCriticalTwoSided(alpha, df float64) float64 {
	logAlpha := math.Log(alpha)
	logCoef := -logBeta(df/2, 0.5) - 0.5*math.Log(df)
	fd := func(t float64) (float64, float64) {
		p := studentTTwoSidedP(t, df)
		logPdf := logCoef - ((df+1)/2)*math.Log1p((t/df)*t)
		return math.Log(p) - logAlpha, -2 * math.Exp(logPdf) / p
	}
	lo, hi := 0.0, 1.0
	for {
		g, _ := fd(hi)
		if g <= 0 {
			break
		}
		lo = hi
		hi *= 4
		if math.IsInf(hi, 0) {
			return math.Inf(1)
		}
	}
	const eps = 0x1p-52
	x := 0.5 * (lo + hi)
	for range 200 {
		f, d := fd(x)
		if f == 0 {
			return x
		}
		if f > 0 {
			lo = x
		} else {
			hi = x
		}
		next := x - f/d
		if math.IsNaN(next) || next <= lo || next >= hi {
			next = 0.5 * (lo + hi)
		}
		if math.Abs(next-x) <= 4*eps*math.Abs(next) || hi-lo <= 4*eps*math.Abs(hi) {
			return next
		}
		x = next
	}
	return x
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
	x, y, lx, ly := studentTBetaArgs(t, df)
	// I_x(df/2, 1/2) is the two-sided tail mass; halve it for one tail.
	tail := 0.5 * regularizedIncompleteBetaLog(df/2.0, 0.5, x, y, lx, ly)
	if t > 0 {
		return 1 - tail
	}
	return tail
}

// betacf evaluates the Lentz continued fraction for the incomplete
// beta integrand. Identical to the processing-package implementation;
// the iteration count grows like O(√max(a, b)).
func betacf(a, b, x float64) float64 {
	const (
		maxIter = 20000
		eps     = 1e-16
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
		if math.Abs(del-1) <= eps {
			return h
		}
	}
	return h
}
