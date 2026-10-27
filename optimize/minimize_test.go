package optimize_test

import (
	"math"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/optimize"
)

// Every objective and constraint here is written FMA-free (each product
// feeding an add is an explicit float64(a*b)), so the solver's inputs —
// and therefore TestOptimize_BitExact's pinned bits — are the same on
// every architecture.

func sq(v float64) float64 { return float64(v * v) }

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Errorf("%s = %.15g, want %.15g (±%g)", what, got, want, tol)
	}
}

func mustCode(t *testing.T, err error, code perr.Code) {
	t.Helper()
	if !perr.HasCode(err, code) {
		t.Fatalf("err = %v, want %s", err, code)
	}
}

// linear returns the constraint a·x + b with its constant gradient a.
func linear(typ optimize.ConstraintType, b float64, a ...float64) optimize.Constraint {
	return optimize.Constraint{
		Type: typ,
		Fn: func(x []float64) float64 {
			s := b
			for i, ai := range a {
				s += float64(ai * x[i])
			}
			return s
		},
		Grad: func([]float64) []float64 { return append([]float64(nil), a...) },
	}
}

// sumSq is Σ (x_i − c_i)² with its gradient.
func sumSq(c ...float64) (func([]float64) float64, func([]float64) []float64) {
	f := func(x []float64) float64 {
		s := 0.0
		for i := range c {
			s += sq(x[i] - c[i])
		}
		return s
	}
	g := func(x []float64) []float64 {
		out := make([]float64, len(c))
		for i := range c {
			out[i] = 2 * (x[i] - c[i])
		}
		return out
	}
	return f, g
}

// TestOptimize_EqualityQuadratic: min x² + y² s.t. x + y = 1 has the
// analytic optimum (½, ½), f* = ½ and multiplier λ = 1 (∇f = λ∇c, and
// f* = b²/2 moves at rate b = 1 as the right-hand side b relaxes). It
// runs unbounded (the LSEI no-inequality QR path) and boxed (the LSI
// path), which must agree.
func TestOptimize_EqualityQuadratic(t *testing.T) {
	t.Parallel()

	f, g := sumSq(0, 0)
	for name, opts := range map[string]optimize.MinimizeOptions{
		"unbounded": {},
		"boxed":     {Lower: []float64{-10, -10}, Upper: []float64{10, 10}},
	} {
		t.Run(name, func(t *testing.T) {
			opts.Constraints = []optimize.Constraint{linear(optimize.Eq, -1, 1, 1)}
			opts.Acc = 1e-12
			res, err := optimize.Minimize(f, g, []float64{3, -2}, opts)
			if err != nil {
				t.Fatal(err)
			}
			near(t, "x", res.X[0], 0.5, 1e-8)
			near(t, "y", res.X[1], 0.5, 1e-8)
			near(t, "f", res.F, 0.5, 1e-10)
			near(t, "lambda", res.Multipliers[0], 1, 1e-6)
			near(t, "violation", res.MaxViolation, 0, 1e-10)
			if res.Status != optimize.StatusSuccess || res.Status.String() != "Optimization terminated successfully" {
				t.Errorf("status %d %q", res.Status, res.Status)
			}
		})
	}
}

// TestOptimize_TwoEqualities: min x² + y² + z² s.t. x + y + z = 1,
// x − y = 0 → x = y = z = ⅓ with λ = (⅔, 0) — two equality rows through
// the RQ reduction.
func TestOptimize_TwoEqualities(t *testing.T) {
	t.Parallel()

	f, g := sumSq(0, 0, 0)
	res, err := optimize.Minimize(f, g, []float64{1, 2, 3}, optimize.MinimizeOptions{
		Acc: 1e-12,
		Constraints: []optimize.Constraint{
			linear(optimize.Eq, -1, 1, 1, 1),
			linear(optimize.Eq, 0, 1, -1, 0),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		near(t, "x", res.X[i], 1.0/3, 1e-8)
	}
	near(t, "lambda0", res.Multipliers[0], 2.0/3, 1e-6)
	near(t, "lambda1", res.Multipliers[1], 0, 1e-6)
}

// TestOptimize_FullyDetermined: as many equalities as variables (the
// me = n basis return) — min (x−3)² + (y+1)² s.t. x + y = 1, x − y = 0
// → (½, ½) with ∇f = (−5, 3) = λ1(1, 1) + λ2(1, −1), λ = (−1, −4).
func TestOptimize_FullyDetermined(t *testing.T) {
	t.Parallel()

	f, g := sumSq(3, -1)
	res, err := optimize.Minimize(f, g, []float64{0, 0}, optimize.MinimizeOptions{
		Acc: 1e-12,
		Constraints: []optimize.Constraint{
			linear(optimize.Eq, -1, 1, 1),
			linear(optimize.Eq, 0, 1, -1),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "x", res.X[0], 0.5, 1e-9)
	near(t, "y", res.X[1], 0.5, 1e-9)
	near(t, "lambda0", res.Multipliers[0], -1, 1e-6)
	near(t, "lambda1", res.Multipliers[1], -4, 1e-6)
}

// TestOptimize_InequalityActive: min (x−2)² + (y−2)² s.t. 1 − x − y ≥ 0
// binds at (½, ½) with λ = 3 ≥ 0; a slack x + 10 ≥ 0 carries λ = 0. The
// constraints are listed slack-first and inequality-before-equality-free
// to prove Multipliers follows the caller's order.
func TestOptimize_InequalityActive(t *testing.T) {
	t.Parallel()

	f, g := sumSq(2, 2)
	res, err := optimize.Minimize(f, g, []float64{0, 0}, optimize.MinimizeOptions{
		Acc: 1e-12,
		Constraints: []optimize.Constraint{
			linear(optimize.Ineq, 10, 1, 0),
			linear(optimize.Ineq, 1, -1, -1),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "x", res.X[0], 0.5, 1e-8)
	near(t, "y", res.X[1], 0.5, 1e-8)
	near(t, "slack lambda", res.Multipliers[0], 0, 1e-8)
	near(t, "active lambda", res.Multipliers[1], 3, 1e-6)
}

// TestOptimize_MixedOrder: an Ineq listed before an Eq — the solver
// takes equalities first, and Multipliers must come back in the
// caller's order. min x² + y² s.t. y − 2 ≥ 0 (binds, λ = 4 at y = 2)
// and x − 1 = 0 (λ = 2 at x = 1).
func TestOptimize_MixedOrder(t *testing.T) {
	t.Parallel()

	f, g := sumSq(0, 0)
	res, err := optimize.Minimize(f, g, []float64{0, 0}, optimize.MinimizeOptions{
		Acc: 1e-12,
		Constraints: []optimize.Constraint{
			linear(optimize.Ineq, -2, 0, 1),
			linear(optimize.Eq, -1, 1, 0),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "x", res.X[0], 1, 1e-8)
	near(t, "y", res.X[1], 2, 1e-8)
	near(t, "ineq lambda", res.Multipliers[0], 4, 1e-6)
	near(t, "eq lambda", res.Multipliers[1], 2, 1e-6)
}

// TestOptimize_BoundActive: min (x−2)² + (y+1)² + (z−3)² over
// 0 ≤ x ≤ 1, y ≥ 0, z free lands on (1, 0, 3): x at its upper bound, y
// at its lower, z inside.
func TestOptimize_BoundActive(t *testing.T) {
	t.Parallel()

	f, g := sumSq(2, -1, 3)
	res, err := optimize.Minimize(f, g, []float64{0.5, 5, 0}, optimize.MinimizeOptions{
		Lower: []float64{0, 0, math.Inf(-1)},
		Upper: []float64{1, math.NaN(), math.Inf(1)},
		Acc:   1e-12,
	})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "x", res.X[0], 1, 1e-12)
	near(t, "y", res.X[1], 0, 1e-12)
	near(t, "z", res.X[2], 3, 1e-8)
	want := []optimize.ActiveBound{optimize.AtUpper, optimize.AtLower, optimize.NotActive}
	for i, w := range want {
		if res.Active[i] != w {
			t.Errorf("Active[%d] = %d, want %d", i, res.Active[i], w)
		}
	}
	if len(res.Multipliers) != 0 {
		t.Errorf("Multipliers %v with no constraints", res.Multipliers)
	}
}

// TestOptimize_InfeasibleStart: a start that violates the inequality
// (and one that violates an equality) still converges to the feasible
// optimum.
func TestOptimize_InfeasibleStart(t *testing.T) {
	t.Parallel()

	f, g := sumSq(2, 2)
	res, err := optimize.Minimize(f, g, []float64{5, 5}, optimize.MinimizeOptions{
		Acc:         1e-12,
		Constraints: []optimize.Constraint{linear(optimize.Ineq, 1, -1, -1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "x", res.X[0], 0.5, 1e-8)
	near(t, "y", res.X[1], 0.5, 1e-8)
	near(t, "lambda", res.Multipliers[0], 3, 1e-6)

	f2, g2 := sumSq(0, 0)
	res, err = optimize.Minimize(f2, g2, []float64{40, -7}, optimize.MinimizeOptions{
		Acc:         1e-12,
		Constraints: []optimize.Constraint{linear(optimize.Eq, -1, 1, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "x", res.X[0], 0.5, 1e-8)
}

// TestOptimize_Infeasible: x − 1 ≥ 0 and −x ≥ 0 admit no point; the run
// returns its final iterate WITH PULSE_OPTIMIZE_INFEASIBLE, never a
// success.
func TestOptimize_Infeasible(t *testing.T) {
	t.Parallel()

	f, g := sumSq(0)
	res, err := optimize.Minimize(f, g, []float64{0.5}, optimize.MinimizeOptions{
		Constraints: []optimize.Constraint{
			linear(optimize.Ineq, -1, 1),
			linear(optimize.Ineq, 0, -1),
		},
	})
	mustCode(t, err, perr.PULSE_OPTIMIZE_INFEASIBLE)
	if res == nil {
		t.Fatal("no result alongside the infeasible error")
	}
	if res.Status == optimize.StatusSuccess || !(res.MaxViolation > 0) {
		t.Errorf("status %v, max violation %v", res.Status, res.MaxViolation)
	}
	var ce *perr.CodedError
	if !asCoded(err, &ce) || ce.Details["status"] != int(res.Status) {
		t.Errorf("details %v", ce)
	}
}

func asCoded(err error, out **perr.CodedError) bool {
	ce, ok := err.(*perr.CodedError)
	*out = ce
	return ok
}

// rosen is the Rosenbrock function, FMA-free.
func rosen(x []float64) float64 {
	return sq(1-x[0]) + float64(100*sq(x[1]-sq(x[0])))
}

func rosenGrad(x []float64) []float64 {
	d := x[1] - sq(x[0])
	return []float64{
		float64(-2*(1-x[0])) - float64(400*float64(x[0]*d)),
		float64(200 * d),
	}
}

// TestOptimize_NotConverged: an iteration cap of 2 on Rosenbrock stops
// short with StatusIterLimit and PULSE_OPTIMIZE_NOT_CONVERGED, the
// iterate returned.
func TestOptimize_NotConverged(t *testing.T) {
	t.Parallel()

	res, err := optimize.Minimize(rosen, rosenGrad, []float64{-1.2, 1}, optimize.MinimizeOptions{MaxIter: 2})
	mustCode(t, err, perr.PULSE_OPTIMIZE_NOT_CONVERGED)
	if res == nil || res.Status != optimize.StatusIterLimit || res.Iterations != 2 {
		t.Fatalf("res %+v", res)
	}
	// Uncapped it converges to (1, 1).
	res, err = optimize.Minimize(rosen, rosenGrad, []float64{-1.2, 1}, optimize.MinimizeOptions{Acc: 1e-12})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "x", res.X[0], 1, 1e-4)
	near(t, "y", res.X[1], 1, 1e-4)
}

// TestOptimize_InvalidInput: every refused input is PULSE_OPTIMIZE_INVALID
// with a nil result and the reason in details — never a panic, including
// a gradient of the wrong length discovered mid-run.
func TestOptimize_InvalidInput(t *testing.T) {
	t.Parallel()

	f, g := sumSq(0, 0)
	ok := linear(optimize.Eq, -1, 1, 1)
	badGrad := func([]float64) []float64 { return []float64{1} }
	cases := []struct {
		name   string
		f      func([]float64) float64
		g      func([]float64) []float64
		x0     []float64
		opts   optimize.MinimizeOptions
		reason string
	}{
		{"nil f", nil, g, []float64{0, 0}, optimize.MinimizeOptions{}, "nil_callback"},
		{"nil grad", f, nil, []float64{0, 0}, optimize.MinimizeOptions{}, "nil_callback"},
		{"empty x0", f, g, nil, optimize.MinimizeOptions{}, "empty_x0"},
		{"nan x0", f, g, []float64{0, math.NaN()}, optimize.MinimizeOptions{}, "non_finite_x0"},
		{"inf x0", f, g, []float64{math.Inf(1), 0}, optimize.MinimizeOptions{}, "non_finite_x0"},
		{"lower length", f, g, []float64{0, 0}, optimize.MinimizeOptions{Lower: []float64{0}}, "bounds_length"},
		{"upper length", f, g, []float64{0, 0}, optimize.MinimizeOptions{Upper: []float64{0, 0, 0}}, "bounds_length"},
		{"crossed", f, g, []float64{0, 0}, optimize.MinimizeOptions{Lower: []float64{0, 2}, Upper: []float64{1, 1}}, "bounds_crossed"},
		{"neg acc", f, g, []float64{0, 0}, optimize.MinimizeOptions{Acc: -1}, "acc"},
		{"nan acc", f, g, []float64{0, 0}, optimize.MinimizeOptions{Acc: math.NaN()}, "acc"},
		{"neg iter", f, g, []float64{0, 0}, optimize.MinimizeOptions{MaxIter: -1}, "max_iter"},
		{"no type", f, g, []float64{0, 0}, optimize.MinimizeOptions{Constraints: []optimize.Constraint{{Fn: ok.Fn, Grad: ok.Grad}}}, "constraint_type"},
		{"nil fn", f, g, []float64{0, 0}, optimize.MinimizeOptions{Constraints: []optimize.Constraint{{Grad: ok.Grad, Type: optimize.Eq}}}, "nil_callback"},
		{"too many eq", f, g, []float64{0}, optimize.MinimizeOptions{Constraints: []optimize.Constraint{ok, ok}}, "too_many_equalities"},
		{"grad length", f, badGrad, []float64{0, 0}, optimize.MinimizeOptions{}, "gradient_length"},
		{"constraint grad length", f, g, []float64{0, 0}, optimize.MinimizeOptions{Constraints: []optimize.Constraint{{Fn: ok.Fn, Grad: badGrad, Type: optimize.Ineq}}}, "gradient_length"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := optimize.Minimize(tc.f, tc.g, tc.x0, tc.opts)
			mustCode(t, err, perr.PULSE_OPTIMIZE_INVALID)
			if res != nil {
				t.Errorf("result %+v alongside an invalid-input error", res)
			}
			var ce *perr.CodedError
			if !asCoded(err, &ce) || ce.Details["reason"] != tc.reason {
				t.Errorf("details %v, want reason %q", ce, tc.reason)
			}
		})
	}
}

// TestOptimize_CallerCannotCorruptIterate: an objective that scribbles on
// its argument sees a scratch copy, so the run is unaffected.
func TestOptimize_CallerCannotCorruptIterate(t *testing.T) {
	t.Parallel()

	f, g := sumSq(2, 2)
	clean, err := optimize.Minimize(f, g, []float64{0, 0}, optimize.MinimizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dirty, err := optimize.Minimize(func(x []float64) float64 {
		v := f(x)
		x[0], x[1] = 1e9, -1e9
		return v
	}, func(x []float64) []float64 {
		v := g(x)
		x[0] = math.NaN()
		return v
	}, []float64{0, 0}, optimize.MinimizeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if clean.X[0] != dirty.X[0] || clean.X[1] != dirty.X[1] || clean.Iterations != dirty.Iterations {
		t.Errorf("clean %v, dirty %v", clean.X, dirty.X)
	}
}

// TestOptimize_BitExact pins one problem's output bits: Rosenbrock under
// a binding equality x·y = 0.4 and a binding inequality x + y ≤ 1.27,
// inside bounds, every input FMA-free, so the
// solver's own FMA-free arithmetic (TestOptimize_NoFMAContraction) makes
// these the bits on every architecture. A dropped float64() barrier, a
// reordered sum or a changed iteration moves them.
func TestOptimize_BitExact(t *testing.T) {
	t.Parallel()

	res, err := optimize.Minimize(rosen, rosenGrad, []float64{0.6, 0.6}, optimize.MinimizeOptions{
		Lower: []float64{-2, -0.5},
		Upper: []float64{0.75, 2},
		Constraints: []optimize.Constraint{
			linear(optimize.Ineq, 1.27, -1, -1),
			{
				Type: optimize.Eq,
				Fn:   func(x []float64) float64 { return float64(x[0]*x[1]) - 0.4 },
				Grad: func(x []float64) []float64 { return []float64{x[1], x[0]} },
			},
		},
		Acc: 1e-10,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := []uint64{
		math.Float64bits(res.X[0]), math.Float64bits(res.X[1]), math.Float64bits(res.F),
		math.Float64bits(res.Multipliers[0]), math.Float64bits(res.Multipliers[1]),
	}
	want := []uint64{0x3fe62322dc25d26b, 0x3fe280b42e179e39, 0x3ff1678ec1c8d489, 0x4071123902368b6f, 0x407a7a2c8c1905d0}
	t.Logf("x=%v f=%v mult=%v iters=%d bits=%#x", res.X, res.F, res.Multipliers, res.Iterations, got)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bit word %d = %#016x, want %#016x", i, got[i], want[i])
		}
	}
	if res.Iterations != 7 {
		t.Errorf("iterations %d", res.Iterations)
	}
}
