package optimize_test

import (
	stderrors "errors"
	"math"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/optimize"
)

// Every residual and Jacobian here is FMA-free and routes exp through
// detmath, so TestOptimize_LeastSquaresBitExact's pinned bits are the
// same on every architecture.

// decayData is y_i = 5·exp(−0.3·t_i) + e_i at t = 0..39 with a fixed,
// zero-mean perturbation of amplitude amp.
func decayData(amp float64) (t, y []float64) {
	for i := 0; i < 40; i++ {
		ti := float64(i) / 2
		e := float64(amp * float64((i*7)%11-5))
		t = append(t, ti)
		y = append(y, float64(5*detmath.Exp(float64(-0.3*ti)))+e)
	}
	return t, y
}

// decayModel is r_i = a·exp(−k·t_i) − y_i over x = (a, k).
func decayModel(t, y []float64) (func([]float64) []float64, func([]float64) [][]float64) {
	r := make([]float64, len(t))
	jac := make([][]float64, len(t))
	for i := range jac {
		jac[i] = make([]float64, 2)
	}
	res := func(x []float64) []float64 {
		for i, ti := range t {
			r[i] = float64(x[0]*detmath.Exp(float64(-x[1]*ti))) - y[i]
		}
		return r
	}
	jf := func(x []float64) [][]float64 {
		for i, ti := range t {
			e := detmath.Exp(float64(-x[1] * ti))
			jac[i][0] = e
			jac[i][1] = -float64(float64(x[0]*ti) * e)
		}
		return jac
	}
	return res, jf
}

// TestOptimize_LeastSquaresExponentialDecay: on exact data the fit
// recovers (a, k) = (5, 0.3) to the stop tolerance with a zero cost; on
// perturbed data it lands within 2% and reports JᵀJ at the solution.
func TestOptimize_LeastSquaresExponentialDecay(t *testing.T) {
	t.Parallel()

	t.Run("exact", func(t *testing.T) {
		ts, y := decayData(0)
		r, j := decayModel(ts, y)
		res, err := optimize.LeastSquares(r, j, []float64{1, 1}, nil, nil, optimize.LeastSquaresOptions{})
		if err != nil {
			t.Fatal(err)
		}
		near(t, "a", res.X[0], 5, 1e-8)
		near(t, "k", res.X[1], 0.3, 1e-9)
		near(t, "cost", res.Cost, 0, 1e-16)
		if !res.Status.Converged() || res.Iterations < 2 || res.FuncEvals < res.Iterations || res.JacEvals < 1 {
			t.Errorf("status %v iterations %d fevals %d jevals %d", res.Status, res.Iterations, res.FuncEvals, res.JacEvals)
		}
	})
	t.Run("perturbed", func(t *testing.T) {
		ts, y := decayData(0.01)
		r, j := decayModel(ts, y)
		res, err := optimize.LeastSquares(r, j, []float64{1, 1}, nil, nil, optimize.LeastSquaresOptions{})
		if err != nil {
			t.Fatal(err)
		}
		near(t, "a", res.X[0], 5, 0.02*5)
		near(t, "k", res.X[1], 0.3, 0.02*0.3)
		// JTJ is JᵀJ at X: rebuild it from the Jacobian there.
		jac := j(res.X)
		for a := 0; a < 2; a++ {
			for b := 0; b < 2; b++ {
				want := 0.0
				for _, row := range jac {
					want += float64(row[a] * row[b])
				}
				near(t, "JTJ", res.JTJ.At(a, b), want, 1e-12*math.Abs(want))
			}
		}
		// The gradient Jᵀr vanishes at the solution.
		rv := r(res.X)
		for a := 0; a < 2; a++ {
			g := 0.0
			for i, row := range jac {
				g += float64(row[a] * rv[i])
			}
			near(t, "gradient", g, 0, 1e-8)
		}
		for i, a := range res.Active {
			if a != optimize.NotActive {
				t.Errorf("Active[%d] = %v on an unbounded fit", i, a)
			}
		}
	})
}

// linearData is y = 2·x1 + 0·x2 + e, where e leans on x2 (−0.05·x2 plus
// an x2-orthogonal wiggle), so the unconstrained β2 is negative while
// the true β2 is 0.
func linearData() (x1, x2, y []float64) {
	for i := 0; i < 30; i++ {
		a := float64(i%5) + 1
		b := float64((i*3)%7) - 3
		e := float64(-0.05*b) + float64(0.01*float64(i%2*2-1))
		x1, x2, y = append(x1, a), append(x2, b), append(y, float64(2*a)+e)
	}
	return x1, x2, y
}

func linearModel(x1, x2, y []float64) (func([]float64) []float64, func([]float64) [][]float64) {
	r := make([]float64, len(y))
	res := func(b []float64) []float64 {
		for i := range y {
			r[i] = float64(b[0]*x1[i]) + float64(b[1]*x2[i]) - y[i]
		}
		return r
	}
	jac := func([]float64) [][]float64 {
		out := make([][]float64, len(y))
		for i := range y {
			out[i] = []float64{x1[i], x2[i]}
		}
		return out
	}
	return res, jac
}

// TestOptimize_LeastSquaresBoundActive: a coefficient bounded below at 0
// whose true value is 0 (and whose free estimate is negative) lands
// EXACTLY on 0, marked AtLower, with the other coefficient at the
// one-predictor least-squares solution; an upper bound below the truth
// is hit exactly the same way.
func TestOptimize_LeastSquaresBoundActive(t *testing.T) {
	t.Parallel()

	x1, x2, y := linearData()
	r, j := linearModel(x1, x2, y)
	inf := math.Inf(1)

	free, err := optimize.LeastSquares(r, j, []float64{1, 1}, nil, nil, optimize.LeastSquaresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !(free.X[1] < -0.01) {
		t.Fatalf("free β2 = %v; the fixture must give a negative unconstrained estimate", free.X[1])
	}

	var sxy, sxx float64
	for i := range y {
		sxy += float64(x1[i] * y[i])
		sxx += float64(x1[i] * x1[i])
	}
	res, err := optimize.LeastSquares(r, j, []float64{1, 1}, []float64{-inf, 0}, []float64{inf, inf}, optimize.LeastSquaresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.X[1] != 0 || res.Active[1] != optimize.AtLower || res.Active[0] != optimize.NotActive {
		t.Fatalf("β2 = %v (active %v), β1 active %v; want exactly 0 at the lower bound", res.X[1], res.Active[1], res.Active[0])
	}
	near(t, "β1", res.X[0], sxy/sxx, 1e-9)

	up, err := optimize.LeastSquares(r, j, []float64{1, 1}, []float64{math.NaN(), 0}, []float64{1.5, inf}, optimize.LeastSquaresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if up.X[0] != 1.5 || up.Active[0] != optimize.AtUpper {
		t.Errorf("β1 = %v (active %v); want exactly 1.5 at the upper bound", up.X[0], up.Active[0])
	}

	// A start outside the bounds is clipped; a fixed parameter stays put.
	fixed, err := optimize.LeastSquares(r, j, []float64{1, -4}, []float64{-inf, 0.25}, []float64{inf, 0.25}, optimize.LeastSquaresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.X[1] != 0.25 || fixed.Active[1] != optimize.AtLower {
		t.Errorf("fixed β2 = %v (active %v)", fixed.X[1], fixed.Active[1])
	}
}

// rosenResiduals is Rosenbrock as least squares: r = (10(y − x²), 1 − x).
func rosenResiduals(x []float64) []float64 {
	return []float64{10 * (x[1] - float64(x[0]*x[0])), 1 - x[0]}
}

func rosenJac(x []float64) [][]float64 {
	return [][]float64{{-20 * x[0], 10}, {-1, 0}}
}

// TestOptimize_LeastSquaresNotConverged: an iteration cap the run cannot
// meet returns the final iterate with LSIterLimit and
// PULSE_OPTIMIZE_NOT_CONVERGED; uncapped, the same problem converges to
// (1, 1).
func TestOptimize_LeastSquaresNotConverged(t *testing.T) {
	t.Parallel()

	res, err := optimize.LeastSquares(rosenResiduals, rosenJac, []float64{-1.2, 1}, nil, nil, optimize.LeastSquaresOptions{MaxIter: 3})
	mustCode(t, err, perr.PULSE_OPTIMIZE_NOT_CONVERGED)
	if res == nil || res.Status != optimize.LSIterLimit || res.Status.Converged() || res.Iterations != 3 || len(res.X) != 2 || res.JTJ == nil {
		t.Fatalf("res = %+v, want the final iterate with LSIterLimit after 3 iterations", res)
	}
	ok, err := optimize.LeastSquares(rosenResiduals, rosenJac, []float64{-1.2, 1}, nil, nil, optimize.LeastSquaresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	near(t, "x", ok.X[0], 1, 1e-8)
	near(t, "y", ok.X[1], 1, 1e-8)
}

// TestOptimize_LeastSquaresRejectsNonFiniteJacobian: a trial point whose
// Jacobian is not finite lies outside the model's domain and is rejected
// like a cost rise — the damping grows and the run converges outside
// the band — never accepted, and never an error.
func TestOptimize_LeastSquaresRejectsNonFiniteJacobian(t *testing.T) {
	t.Parallel()

	inBand := func(v float64) bool { return v > 1.99 && v < 2.01 }
	res, err := optimize.LeastSquares(
		func(x []float64) []float64 { return []float64{x[0] - 2} },
		func(x []float64) [][]float64 {
			if inBand(x[0]) {
				return [][]float64{{math.NaN()}}
			}
			return [][]float64{{1}}
		},
		[]float64{0}, nil, nil, optimize.LeastSquaresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if inBand(res.X[0]) || !(res.X[0] > 1.9) || !res.Status.Converged() {
		t.Errorf("x = %v (status %v); want converged just below the non-finite band", res.X[0], res.Status)
	}
}

// TestOptimize_LeastSquaresInvalid: bad input is PULSE_OPTIMIZE_INVALID
// with a nil result and the fault in details.reason — callback shape
// faults included, never a panic.
func TestOptimize_LeastSquaresInvalid(t *testing.T) {
	t.Parallel()

	r, j := rosenResiduals, rosenJac
	x0 := []float64{-1.2, 1}
	cases := []struct {
		name   string
		run    func() (*optimize.LeastSquaresResult, error)
		reason string
	}{
		{"nil residual", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(nil, j, x0, nil, nil, optimize.LeastSquaresOptions{})
		}, "nil_callback"},
		{"empty x0", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, j, nil, nil, nil, optimize.LeastSquaresOptions{})
		}, "empty_x0"},
		{"nan x0", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, j, []float64{math.NaN(), 1}, nil, nil, optimize.LeastSquaresOptions{})
		}, "non_finite_x0"},
		{"lower length", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, j, x0, []float64{0}, nil, optimize.LeastSquaresOptions{})
		}, "bounds_length"},
		{"crossed", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, j, x0, []float64{0, 2}, []float64{1, 1}, optimize.LeastSquaresOptions{})
		}, "bounds_crossed"},
		{"max iter", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, j, x0, nil, nil, optimize.LeastSquaresOptions{MaxIter: -1})
		}, "max_iter"},
		{"cost tol", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, j, x0, nil, nil, optimize.LeastSquaresOptions{CostTol: math.Inf(1)})
		}, "cost_tol"},
		{"step tol", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, j, x0, nil, nil, optimize.LeastSquaresOptions{StepTol: -1})
		}, "step_tol"},
		{"no residuals", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(func([]float64) []float64 { return nil }, j, x0, nil, nil, optimize.LeastSquaresOptions{})
		}, "empty_residual"},
		{"non-finite start", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(func([]float64) []float64 { return []float64{math.Inf(1), 0} }, j, x0, nil, nil, optimize.LeastSquaresOptions{})
		}, "non_finite_start"},
		{"residual length changes", func() (*optimize.LeastSquaresResult, error) {
			calls := 0
			return optimize.LeastSquares(func(x []float64) []float64 {
				calls++
				if calls > 1 {
					return []float64{1}
				}
				return r(x)
			}, j, x0, nil, nil, optimize.LeastSquaresOptions{})
		}, "residual_length"},
		{"jacobian rows", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, func([]float64) [][]float64 { return [][]float64{{1, 0}} }, x0, nil, nil, optimize.LeastSquaresOptions{})
		}, "jacobian_shape"},
		{"jacobian nan", func() (*optimize.LeastSquaresResult, error) {
			return optimize.LeastSquares(r, func([]float64) [][]float64 { return [][]float64{{math.NaN(), 0}, {1, 0}} }, x0, nil, nil, optimize.LeastSquaresOptions{})
		}, "non_finite_jacobian"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := c.run()
			mustCode(t, err, perr.PULSE_OPTIMIZE_INVALID)
			if res != nil {
				t.Errorf("result %+v, want nil", res)
			}
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Details["reason"] != c.reason {
				t.Errorf("details = %v, want reason %q", err, c.reason)
			}
		})
	}
}

// TestOptimize_LeastSquaresBitExact pins the perturbed exponential-decay
// fit's bits. The residuals are FMA-free with detmath's exp, and the
// solver is FMA-free (TestOptimize_NoFMAContraction), so these are the
// bits on every architecture; a dropped float64() barrier, a reordered
// sum or a changed damping schedule moves them.
func TestOptimize_LeastSquaresBitExact(t *testing.T) {
	t.Parallel()

	ts, y := decayData(0.01)
	r, j := decayModel(ts, y)
	res, err := optimize.LeastSquares(r, j, []float64{1, 1}, []float64{0, 0}, nil, optimize.LeastSquaresOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := []uint64{
		math.Float64bits(res.X[0]), math.Float64bits(res.X[1]), math.Float64bits(res.Cost),
		math.Float64bits(res.JTJ.At(0, 1)), uint64(res.Iterations),
	}
	want := []uint64{0x4013f3acaa82af94, 0x3fd3225128245ae2, 0x3f95021e3abc4b1c, 0xc03bb15500353ab9, 11}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("bits[%d] = %#x, want %#x", i, got[i], want[i])
		}
	}
}
