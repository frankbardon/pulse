package mcmc

import (
	"math"

	"github.com/frankbardon/pulse/linalg/detmath"
)

// Transform maps an unconstrained x ∈ R to a constrained y = f(x).
//
// Forward returns y with the derivative terms a model needs: it adds
// LogJac = log|dy/dx| to its log density, and chains a gradient through
// DyDx and DLogJac. Inverse returns f⁻¹(y); a y outside the support gives
// NaN or ±Inf. Inverse(Forward(x).Y) == x up to rounding.
type Transform interface {
	Forward(x float64) Mapped
	Inverse(y float64) float64
}

// Mapped is a Transform's value at one unconstrained point.
type Mapped struct {
	// Y is the constrained value f(x).
	Y float64
	// DyDx is f'(x).
	DyDx float64
	// LogJac is log|f'(x)|, the log-Jacobian of the change of variables.
	LogJac float64
	// DLogJac is d LogJac / dx.
	DLogJac float64
}

// Positive maps R to (0, ∞) by y = exp(x).
type Positive struct{}

// Forward returns exp(x); LogJac = x.
func (Positive) Forward(x float64) Mapped {
	y := detmath.Exp(x)
	return Mapped{Y: y, DyDx: y, LogJac: x, DLogJac: 1}
}

// Inverse returns log(y).
func (Positive) Inverse(y float64) float64 { return detmath.Log(y) }

// Unit maps R to (0, 1) by the logistic function y = 1 / (1 + exp(−x)).
type Unit struct{}

// Forward returns logistic(x); LogJac = log y + log(1 − y), computed
// without cancellation in either tail.
func (Unit) Forward(x float64) Mapped {
	u, v, lj := logistic(x)
	return Mapped{Y: u, DyDx: float64(u * v), LogJac: lj, DLogJac: v - u}
}

// Inverse returns logit(y) = log(y) − log(1 − y).
func (Unit) Inverse(y float64) float64 { return detmath.Log(y) - detmath.Log1p(-y) }

// Bounded maps R to (Lo, Hi) by y = Lo + (Hi − Lo)·logistic(x). Lo and
// Hi must be finite with Lo < Hi; otherwise every value is NaN.
type Bounded struct {
	Lo, Hi float64
}

func (b Bounded) valid() bool {
	return b.Lo < b.Hi && !math.IsInf(b.Lo, 0) && !math.IsInf(b.Hi, 0)
}

// Forward returns Lo + (Hi − Lo)·logistic(x), formed from the nearer
// bound so Y never leaves [Lo, Hi]; LogJac = log(Hi − Lo) + log u + log(1 − u) with u = logistic(x).
func (b Bounded) Forward(x float64) Mapped {
	if !b.valid() {
		nan := math.NaN()
		return Mapped{Y: nan, DyDx: nan, LogJac: nan, DLogJac: nan}
	}
	u, v, lj := logistic(x)
	w := b.Hi - b.Lo
	var y float64
	if u <= 0.5 {
		y = b.Lo + float64(w*u)
	} else {
		y = b.Hi - float64(w*v)
	}
	return Mapped{
		Y:       y,
		DyDx:    float64(float64(w*u) * v),
		LogJac:  detmath.Log(w) + lj,
		DLogJac: v - u,
	}
}

// Inverse returns log(y − Lo) − log(Hi − y).
func (b Bounded) Inverse(y float64) float64 {
	if !b.valid() {
		return math.NaN()
	}
	return detmath.Log(y-b.Lo) - detmath.Log(b.Hi-y)
}

// logistic returns u = 1/(1+e^−x), v = 1 − u and log(u·v), each formed
// from e^−|x| so neither tail cancels.
func logistic(x float64) (u, v, logUV float64) {
	ax := math.Abs(x)
	e := detmath.Exp(-ax)
	big := 1 / (1 + e)
	small := e / (1 + e)
	if x >= 0 {
		u, v = big, small
	} else {
		u, v = small, big
	}
	return u, v, -ax - float64(2*detmath.Log1p(e))
}
