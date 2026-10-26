package mcmc_test

import (
	"github.com/frankbardon/pulse/linalg/detmath"
)

// The test targets below are written FMA-free (every product feeding an
// add is an explicit float64(...)) and with detmath only, like the
// package itself: the golden draw hashes in nuts_test.go are computed
// through them, so a contraction here would change the hash on arm64
// only. TestNUTS_TestModelsNoFMAContraction holds the barriers.

// normal is independent N(0, sd_i²) in each coordinate; sd nil means 1.
type normal struct {
	d  int
	sd []float64
}

func (m normal) Dim() int { return m.d }

func (m normal) LogDensityGrad(theta, grad []float64) float64 {
	var s float64
	for i, x := range theta {
		z := x
		if m.sd != nil {
			z = x / m.sd[i]
			grad[i] = -(z / m.sd[i])
		} else {
			grad[i] = -x
		}
		s += float64(z * z)
	}
	return float64(-0.5 * s)
}

// gaussian is N(0, Σ) given its precision P = Σ⁻¹ (row-major d×d).
type gaussian struct {
	d    int
	prec []float64
}

func (m gaussian) Dim() int { return m.d }

func (m gaussian) LogDensityGrad(theta, grad []float64) float64 {
	var lp float64
	for i := range m.d {
		var s float64
		for j := range m.d {
			s += float64(m.prec[i*m.d+j] * theta[j])
		}
		grad[i] = -s
		lp += float64(theta[i] * s)
	}
	return float64(-0.5 * lp)
}

// funnel is Neal's funnel: v ~ N(0, 3²), x_k | v ~ N(0, e^v) for k < d−1,
// with theta = (v, x_1, …): the centred parameterisation whose neck
// makes a fixed step size diverge.
type funnel struct{ d int }

func (m funnel) Dim() int { return m.d }

func (m funnel) LogDensityGrad(theta, grad []float64) float64 {
	v := theta[0]
	ev := detmath.Exp(-v)
	k := float64(m.d - 1)
	lp := float64(-v*v) / 18
	dv := -(v / 9)
	for i := 1; i < m.d; i++ {
		x := theta[i]
		q := float64(float64(x*x) * ev)
		lp -= float64(0.5 * q)
		dv += float64(0.5 * q)
		grad[i] = -float64(x * ev)
	}
	lp -= float64(float64(0.5*k) * v)
	grad[0] = dv - float64(0.5*k)
	return lp
}

// ar1Precision is the tridiagonal precision of the AR(1) correlation
// matrix Σ_ij = rho^|i−j| (unit variances).
func ar1Precision(d int, rho float64) []float64 {
	p := make([]float64, d*d)
	r2 := float64(rho * rho)
	c := 1 / (1 - r2)
	for i := range d {
		diag := float64((1 + r2) * c)
		if i == 0 || i == d-1 {
			diag = c
		}
		p[i*d+i] = diag
		if i+1 < d {
			p[i*d+i+1] = -float64(rho * c)
			p[(i+1)*d+i] = -float64(rho * c)
		}
	}
	return p
}
