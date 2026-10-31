package statdist

import (
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// NormalQuantileDet is NormalQuantile on the bit contract (doc.go): the
// same Acklam seed and erfc-based Halley polish, run on linalg/detmath's
// Erfc / Exp / Log with every product that feeds an add or subtract an
// explicit float64(...) barrier, so its bits are the same on every
// GOARCH (TestStatdist_NormalQuantileDetBitsPinned,
// TestStatdist_NoFMAContraction). It is the deterministic replacement
// for gonum's distuv.UnitNormal.Quantile (agreement: normal_fmafree_test.go)
// and R's qnorm (reference_oracle_test.go). NormalQuantile keeps its
// math.* body, and so its current figures, until its callers migrate.
// ±Inf at p∈{0,1}; NaN for NaN.
func NormalQuantileDet(p float64) float64 {
	switch {
	case math.IsNaN(p):
		return math.NaN()
	case p <= 0:
		return math.Inf(-1)
	case p >= 1:
		return math.Inf(1)
	case p > 0.5:
		return -NormalQuantileDet(1 - p)
	}
	x := acklamNormalQuantileDet(p)
	e := float64(0.5*detmath.Erfc(-x/math.Sqrt2)) - p
	u := e * math.Sqrt(2*math.Pi) * detmath.Exp(0.5*x*x)
	if !math.IsInf(u, 0) && !math.IsNaN(u) {
		x -= u / (1 + float64(0.5*x*u))
	}
	return x
}

// acklamNormalQuantileDet is acklamNormalQuantile with barriered Horner
// steps and detmath.Log, for p in (0, 0.5].
func acklamNormalQuantileDet(p float64) float64 {
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
	if p < pLow {
		q := math.Sqrt(-2 * detmath.Log(p))
		num := horner(c[:], q)
		den := float64(horner(d[:], q)*q) + 1
		return num / den
	}
	q := p - 0.5
	r := q * q
	num := horner(a[:], r) * q
	den := float64(horner(b[:], r)*r) + 1
	return num / den
}

// horner evaluates c[0]·xⁿ⁻¹ + … + c[n−1] with each product barriered.
func horner(c []float64, x float64) float64 {
	s := c[0]
	for _, k := range c[1:] {
		s = float64(s*x) + k
	}
	return s
}
