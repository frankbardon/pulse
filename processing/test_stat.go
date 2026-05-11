package processing

import "math"

// Statistical helpers shared by the TEST_* operators. Keeping these
// dependency-free (no external math packages) avoids pulling in a large
// stats SDK for a handful of well-known distributions.

// studentTTwoSidedP returns the two-sided p-value of a Student's t
// statistic with df degrees of freedom: P(|T| ≥ |t|).
//
// Derivation: the survival function of |T| equals the regularized
// incomplete beta I_x(df/2, 1/2) evaluated at x = df / (df + t²).
//
// Returns NaN for df ≤ 0 and 1 for t = 0.
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

// regularizedIncompleteBeta returns I_x(a, b), the regularized
// incomplete beta function, using the continued-fraction expansion from
// Numerical Recipes (3rd ed.) Section 6.4. Convergence is fast for the
// arguments encountered by the t- and F-distribution survival functions.
//
// For x = 0 or x = 1, returns the closed-form boundary value.
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

// betacf is the Lentz continued-fraction evaluation of the incomplete
// beta. Mirrors the canonical Numerical Recipes routine; converges
// within ~50 iterations for the arguments used here.
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
		mf := float64(m)
		m2 := float64(2 * m)
		// Even step
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
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
		// Odd step
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
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
			break
		}
	}
	return h
}

// studentTInverseTwoSided returns the t quantile such that
// P(|T| ≥ q) = alpha for df degrees of freedom. Used to build the
// confidence interval bounds around a mean difference.
//
// Implemented via a bracketed bisection on studentTTwoSidedP; the search
// range scales with sqrt(df) which covers practical CI use. Returns NaN
// on degenerate inputs.
func studentTInverseTwoSided(alpha, df float64) float64 {
	if df <= 0 || alpha <= 0 || alpha >= 1 {
		return math.NaN()
	}
	// Bracket: |t| grows roughly with 1/sqrt(alpha); 200 is comfortably
	// past every CI tail used in practice for df ≥ 1.
	lo, hi := 0.0, 200.0
	for range 100 {
		mid := 0.5 * (lo + hi)
		p := studentTTwoSidedP(mid, df)
		if p > alpha {
			lo = mid
		} else {
			hi = mid
		}
		if hi-lo < 1e-9 {
			break
		}
	}
	return 0.5 * (lo + hi)
}
