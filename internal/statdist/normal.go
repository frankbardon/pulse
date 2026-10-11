package statdist

import "math"

// NormalQuantile returns Φ⁻¹(p), the standard normal quantile:
// Acklam's rational approximation (~1e-9 relative) polished by one
// Halley step against the erfc-based Φ, which brings it to full double
// precision across the support (p down to ~1e-300; checked against R's
// qnorm by internal/processing's reference_oracle_test.go through its
// standardNormalPPF wrapper). The upper half is solved as −Φ⁻¹(1 − p),
// 1 − p being exact there. ±Inf at p∈{0,1}; NaN for NaN.
func NormalQuantile(p float64) float64 {
	switch {
	case math.IsNaN(p):
		return math.NaN()
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	case p > 0.5:
		return -NormalQuantile(1 - p)
	}
	x := acklamNormalQuantile(p)
	// Halley step on Φ(x) − p, with Φ(x) = ½ erfc(−x/√2) accurate to a
	// relative few ulp in this (lower) half.
	e := 0.5*math.Erfc(-x/math.Sqrt2) - p
	u := e * math.Sqrt(2*math.Pi) * math.Exp(0.5*x*x)
	if !math.IsInf(u, 0) && !math.IsNaN(u) {
		x -= u / (1 + 0.5*x*u)
	}
	return x
}

// acklamNormalQuantile is Acklam's rational approximation to Φ⁻¹(p) (~1e-9
// relative), the seed for NormalQuantile.
func acklamNormalQuantile(p float64) float64 {
	a := [...]float64{
		-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00,
	}
	b := [...]float64{
		-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01,
	}
	c := [...]float64{
		-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00,
	}
	d := [...]float64{
		7.784695709041462e-03, 3.224671290700398e-01, 2.445134137142996e+00,
		3.754408661907416e+00,
	}
	const pLow = 0.02425
	const pHigh = 1 - pLow
	switch {
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	case p < pLow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p <= pHigh:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	}
}
