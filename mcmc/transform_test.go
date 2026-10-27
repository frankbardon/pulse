package mcmc_test

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/mcmc"
)

var transforms = map[string]mcmc.Transform{
	"positive":     mcmc.Positive{},
	"unit":         mcmc.Unit{},
	"bounded":      mcmc.Bounded{Lo: -3, Hi: 5},
	"bounded_tiny": mcmc.Bounded{Lo: 1e-3, Hi: 2e-3},
}

var grid = []float64{-30, -12, -4, -1.5, -0.3, 0, 0.2, 1, 2.5, 7, 15, 30}

// TestMCMC_TransformRoundTrip: Inverse(Forward(x).Y) recovers x and
// Forward(Inverse(y)) recovers y, and Y stays inside the support.
func TestMCMC_TransformRoundTrip(t *testing.T) {
	t.Parallel()

	for name, tr := range transforms {
		for _, x := range grid {
			if name != "positive" && math.Abs(x) > 15 {
				// logistic(±30) rounds to the bound: Y is exact there but
				// no longer invertible to x.
				continue
			}
			y := tr.Forward(x).Y
			back := tr.Inverse(y)
			if math.Abs(back-x) > 1e-9*math.Max(1, math.Abs(x)) {
				t.Errorf("%s: Inverse(Forward(%g)) = %.17g", name, x, back)
			}
			if again := tr.Forward(back).Y; math.Abs(again-y) > 1e-14*math.Max(1, math.Abs(y)) {
				t.Errorf("%s: Forward(Inverse(%g)) = %.17g", name, y, again)
			}
		}
		for _, x := range grid {
			y := tr.Forward(x).Y
			switch b := tr.(type) {
			case mcmc.Positive:
				if !(y > 0) {
					t.Errorf("positive: Forward(%g) = %g", x, y)
				}
			case mcmc.Unit:
				if !(y >= 0 && y <= 1) {
					t.Errorf("unit: Forward(%g) = %g", x, y)
				}
			case mcmc.Bounded:
				if !(y >= b.Lo && y <= b.Hi) {
					t.Errorf("%s: Forward(%g) = %g outside [%g, %g]", name, x, y, b.Lo, b.Hi)
				}
			}
		}
	}
}

// TestMCMC_TransformJacobianMatchesFiniteDifference: DyDx, LogJac and
// DLogJac agree with central differences of Y and LogJac.
func TestMCMC_TransformJacobianMatchesFiniteDifference(t *testing.T) {
	t.Parallel()

	const h = 1e-5
	for name, tr := range transforms {
		for _, x := range []float64{-6, -2, -0.7, 0, 0.4, 1.3, 3, 6} {
			m := tr.Forward(x)
			up, dn := tr.Forward(x+h), tr.Forward(x-h)
			dy := (up.Y - dn.Y) / (2 * h)
			if rel(m.DyDx, dy) > 1e-6 {
				t.Errorf("%s at %g: DyDx = %.12g, finite difference %.12g", name, x, m.DyDx, dy)
			}
			if lj := math.Log(math.Abs(dy)); math.Abs(m.LogJac-lj) > 1e-6 {
				t.Errorf("%s at %g: LogJac = %.12g, log|finite difference| %.12g", name, x, m.LogJac, lj)
			}
			if dlj := (up.LogJac - dn.LogJac) / (2 * h); math.Abs(m.DLogJac-dlj) > 1e-6 {
				t.Errorf("%s at %g: DLogJac = %.12g, finite difference %.12g", name, x, m.DLogJac, dlj)
			}
		}
	}
}

// TestMCMC_TransformTailsStayFinite: the logistic log-Jacobian holds in
// both tails where the naive log(y) + log(1 − y) would be −Inf.
func TestMCMC_TransformTailsStayFinite(t *testing.T) {
	t.Parallel()

	for _, x := range []float64{-800, 800} {
		m := mcmc.Unit{}.Forward(x)
		if math.Abs(m.LogJac-(-800)) > 1e-12 {
			t.Errorf("Unit.Forward(%g).LogJac = %g, want -800", x, m.LogJac)
		}
		if math.Abs(m.DLogJac) != 1 {
			t.Errorf("Unit.Forward(%g).DLogJac = %g, want ∓1", x, m.DLogJac)
		}
	}
	// Close to 1 the complement keeps full precision.
	m := mcmc.Unit{}.Forward(20)
	if want := -20 - 2*math.Log1p(math.Exp(-20)); math.Abs(m.LogJac-want) > 1e-12 {
		t.Errorf("Unit.Forward(20).LogJac = %.17g, want %.17g", m.LogJac, want)
	}
}

// TestMCMC_BoundedRefusesBadBounds: unordered or infinite bounds give NaN.
func TestMCMC_BoundedRefusesBadBounds(t *testing.T) {
	t.Parallel()

	for _, b := range []mcmc.Bounded{{Lo: 1, Hi: 1}, {Lo: 2, Hi: 1}, {Lo: 0, Hi: math.Inf(1)}, {Lo: math.NaN(), Hi: 1}} {
		m := b.Forward(0.3)
		if !math.IsNaN(m.Y) || !math.IsNaN(m.LogJac) || !math.IsNaN(b.Inverse(0.5)) {
			t.Errorf("%+v: Forward = %+v, Inverse = %g, want NaN", b, m, b.Inverse(0.5))
		}
	}
}

func rel(a, b float64) float64 {
	return math.Abs(a-b) / math.Max(math.Abs(b), 1e-300)
}
