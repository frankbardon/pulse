package optimize

import (
	"math"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
)

// LeastSquaresOptions configures LeastSquares. The zero value is the
// default schedule.
type LeastSquaresOptions struct {
	// MaxIter caps the outer iterations (one per accepted step, the
	// damping retries inside an iteration not counted). 0 means 2000;
	// negative is refused.
	MaxIter int
	// CostTol stops the run when an accepted step lowers the cost by at
	// most CostTol·cost. 0 means 1e-13; a negative or non-finite value is
	// refused.
	CostTol float64
	// StepTol stops the run when an accepted step moves every parameter
	// by at most StepTol·(1+|x_i|). 0 means 1e-10; a negative or
	// non-finite value is refused.
	StepTol float64
}

// LeastSquaresStatus says why LeastSquares stopped. The zero value is
// never returned.
type LeastSquaresStatus int

const (
	// LSConvergedCost: an accepted step lowered the cost by at most
	// CostTol relative.
	LSConvergedCost LeastSquaresStatus = iota + 1
	// LSConvergedStep: an accepted step moved every parameter by at most
	// StepTol relative, or every parameter is held on a bound.
	LSConvergedStep
	// LSConvergedDamping: no step lowered the cost at any damping up to
	// 1e16 — x is a minimum to working precision.
	LSConvergedDamping
	// LSIterLimit: MaxIter iterations ran without meeting a stop rule.
	LSIterLimit
)

var lsStatusMessages = map[LeastSquaresStatus]string{
	LSConvergedCost:    "relative cost change below CostTol",
	LSConvergedStep:    "relative step below StepTol",
	LSConvergedDamping: "no descent at any damping up to 1e16",
	LSIterLimit:        "iteration limit reached",
}

// String describes the stop.
func (s LeastSquaresStatus) String() string {
	if m, ok := lsStatusMessages[s]; ok {
		return m
	}
	return "unknown status"
}

// Converged reports whether the run met a stop rule (every status but
// LSIterLimit).
func (s LeastSquaresStatus) Converged() bool {
	return s == LSConvergedCost || s == LSConvergedStep || s == LSConvergedDamping
}

// LeastSquaresResult is LeastSquares' final iterate.
type LeastSquaresResult struct {
	// X is the final parameter vector; Cost is ½·Σ r_i² there.
	X    []float64
	Cost float64
	// JTJ is JᵀJ at X over every parameter (bound-held ones included),
	// so a caller forms the Gauss–Newton covariance σ²·(JᵀJ)⁻¹ from it
	// (linalg.InverseSPD), with σ² = 2·Cost / (m − n), after dropping the
	// rows and columns of parameters held on a bound.
	JTJ *linalg.Sym
	// Active marks, per parameter, the bound it sits EXACTLY on: a step
	// that crosses a bound is projected onto it, so a parameter bounded
	// at 0 reaches 0.
	Active     []ActiveBound
	Status     LeastSquaresStatus
	Iterations int
	FuncEvals  int
	JacEvals   int
}

// LeastSquares minimises ½·Σ r_i(x)² by projected Levenberg–Marquardt
// from x0 within per-parameter bounds lower ≤ x ≤ upper (each nil, or
// one entry per parameter; an infinite or NaN entry leaves that side
// open). residual returns the m residuals at x (m fixed by the first
// call); jacobian returns the m×n Jacobian ∂r_i/∂x_j as m rows. Both
// see a scratch copy of x and may reuse their returned slices.
//
// The schedule is fixed: Marquardt damping λ·max(diag(JᵀJ), 1e-12)
// with λ starting at 1e-3, shrunk ×⅓ (floor 1e-12) on an accepted step
// and grown ×4 on a rejected one. Each iteration holds a parameter that
// sits on a bound its gradient pushes past, solves the damped normal
// equations on the rest (linalg.SolveSPD), and projects the trial point
// onto the bounds — so a bound is reached exactly, never approached
// through a reparametrisation. A trial is accepted when its cost does
// not rise. The run stops on an accepted step that changes the cost by
// ≤ CostTol relative or every parameter by ≤ StepTol·(1+|x|), or when λ
// exceeds 1e16. The arithmetic is serial, fixed-order and FMA-free.
//
// Bad input (nil callbacks, an empty or non-finite x0, a bound slice of
// the wrong length or crossed bounds, an option out of range, no
// residuals, a residual or Jacobian of the wrong shape, a non-finite
// residual at the start or a non-finite Jacobian entry) is a
// PULSE_OPTIMIZE_INVALID error with a nil result. An iteration-limit
// stop returns the final iterate WITH PULSE_OPTIMIZE_NOT_CONVERGED; a
// converged result has a nil error. x0 is clipped to the bounds.
func LeastSquares(residual func(x []float64) []float64, jacobian func(x []float64) [][]float64,
	x0, lower, upper []float64, opts LeastSquaresOptions) (res *LeastSquaresResult, err error) {
	if err := validateLeastSquares(residual, jacobian, x0, lower, upper, opts); err != nil {
		return nil, err
	}
	n := len(x0)
	maxIter := opts.MaxIter
	if maxIter == 0 {
		maxIter = 2000
	}
	costTol, stepTol := opts.CostTol, opts.StepTol
	if costTol == 0 {
		costTol = 1e-13
	}
	if stepTol == 0 {
		stepTol = 1e-10
	}
	lo, hi := boundVector(lower, n), boundVector(upper, n)

	defer func() {
		if r := recover(); r != nil {
			be, ok := r.(badEval)
			if !ok {
				panic(r)
			}
			res, err = nil, be.err
		}
	}()

	xs := make([]float64, n)
	call := func(x []float64) []float64 { copy(xs, x); return xs }
	nfev, njev := 0, 0
	m := -1
	evalR := func(x, dst []float64) []float64 {
		nfev++
		got := residual(call(x))
		if m < 0 {
			m = len(got)
			if m == 0 {
				panic(badEval{perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_INVALID,
					"residual returned no residuals", map[string]any{"reason": "empty_residual"})})
			}
			dst = make([]float64, m)
		}
		if len(got) != m {
			panic(badEval{perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_INVALID,
				"residual returned a vector of the wrong length",
				map[string]any{"reason": "residual_length", "want": m, "got": len(got)})})
		}
		copy(dst, got)
		return dst
	}
	// evalJ fills dst with the Jacobian at x; finite is false when an
	// entry is NaN or infinite (dst is then partly written).
	evalJ := func(x []float64, dst [][]float64) (finite bool) {
		njev++
		got := jacobian(call(x))
		if len(got) != m {
			panic(badEval{perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_INVALID,
				"jacobian returned the wrong number of rows",
				map[string]any{"reason": "jacobian_shape", "want_rows": m, "got_rows": len(got)})})
		}
		for i, row := range got {
			if len(row) != n {
				panic(badEval{perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_INVALID,
					"a jacobian row has the wrong length",
					map[string]any{"reason": "jacobian_shape", "row": i, "want": n, "got": len(row)})})
			}
			for _, v := range row {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return false
				}
			}
			copy(dst[i], row)
		}
		return true
	}

	x := make([]float64, n)
	for i, v := range x0 {
		x[i] = project(v, lo[i], hi[i])
	}
	r := evalR(x, nil)
	cost := halfSumSq(r)
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		return nil, invalid("the residual at the start is not finite", map[string]any{"reason": "non_finite_start"})
	}
	rTry := make([]float64, m)
	jac, jTry := make([][]float64, m), make([][]float64, m)
	for i := range jac {
		jac[i], jTry[i] = make([]float64, n), make([]float64, n)
	}
	if !evalJ(x, jac) {
		return nil, invalid("the jacobian at the start is not finite", map[string]any{"reason": "non_finite_jacobian"})
	}

	finish := func(status LeastSquaresStatus, iter int) *LeastSquaresResult {
		out := &LeastSquaresResult{
			X: x, Cost: cost, Active: make([]ActiveBound, n), Status: status,
			Iterations: iter, FuncEvals: nfev, JacEvals: njev,
		}
		jtj, _ := normalEquations(jac, r, n)
		packed := make([]float64, 0, n*(n+1)/2)
		for a := 0; a < n; a++ {
			packed = append(packed, jtj[a][a:]...)
		}
		out.JTJ, _ = linalg.NewSym(n, packed)
		for i, xi := range x {
			switch {
			case xi == lo[i]:
				out.Active[i] = AtLower
			case xi == hi[i]:
				out.Active[i] = AtUpper
			}
		}
		return out
	}

	lambda := 1e-3
	free := make([]int, 0, n)
	try := make([]float64, n)
	for iter := 1; iter <= maxIter; iter++ {
		jtj, jtr := normalEquations(jac, r, n)
		// Hold a parameter on a bound the gradient pushes past (or a
		// fixed one); the rest move.
		free = free[:0]
		for a := 0; a < n; a++ {
			switch {
			case lo[a] == hi[a]:
			case x[a] == lo[a] && jtr[a] > 0:
			case x[a] == hi[a] && jtr[a] < 0:
			default:
				free = append(free, a)
			}
		}
		if len(free) == 0 {
			return finish(LSConvergedStep, iter), nil
		}
		nf := len(free)
		packed := make([]float64, nf*(nf+1)/2)
		rhs := make([]float64, nf)
		for {
			k := 0
			for i, a := range free {
				rhs[i] = -jtr[a]
				for j := i; j < nf; j++ {
					packed[k] = jtj[a][free[j]]
					if j == i {
						packed[k] += float64(lambda * math.Max(jtj[a][a], 1e-12))
					}
					k++
				}
			}
			if step, ok := solveDamped(nf, packed, rhs); ok {
				copy(try, x)
				for i, a := range free {
					try[a] = project(x[a]+step[i], lo[a], hi[a])
				}
				rTry = evalR(try, rTry)
				// A trial is accepted when its cost does not rise and its
				// Jacobian is finite (a point outside the model's domain is
				// rejected like a rise).
				if c := halfSumSq(rTry); c <= cost && !math.IsNaN(c) && evalJ(try, jTry) {
					done := cost-c <= float64(costTol*math.Max(cost, 1e-300))
					small := true
					for a := range try {
						small = small && math.Abs(try[a]-x[a]) <= float64(stepTol*(1+math.Abs(x[a])))
					}
					x, try = try, x
					r, rTry = rTry, r
					jac, jTry = jTry, jac
					cost = c
					lambda = math.Max(lambda/3, 1e-12)
					switch {
					case done:
						return finish(LSConvergedCost, iter), nil
					case small:
						return finish(LSConvergedStep, iter), nil
					}
					break
				}
			}
			lambda *= 4
			if lambda > 1e16 {
				return finish(LSConvergedDamping, iter), nil
			}
		}
	}
	out := finish(LSIterLimit, maxIter)
	return out, perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_NOT_CONVERGED,
		"the least-squares solver stopped before converging: "+LSIterLimit.String(),
		map[string]any{"status": int(LSIterLimit), "message": LSIterLimit.String(),
			"iterations": maxIter, "cost": cost})
}

// project clips v into [lo, hi]; a NaN bound is open.
func project(v, lo, hi float64) float64 {
	if !math.IsNaN(lo) && v < lo {
		return lo
	}
	if !math.IsNaN(hi) && v > hi {
		return hi
	}
	return v
}

// halfSumSq is ½·Σ r_i², summed in index order.
func halfSumSq(r []float64) float64 {
	f := 0.0
	for _, v := range r {
		f += float64(v * v)
	}
	return float64(0.5 * f)
}

// normalEquations returns JᵀJ (full, symmetric) and Jᵀr, accumulated
// row by row in index order.
func normalEquations(jac [][]float64, r []float64, n int) ([][]float64, []float64) {
	jtj := make([][]float64, n)
	for a := range jtj {
		jtj[a] = make([]float64, n)
	}
	jtr := make([]float64, n)
	for i, row := range jac {
		for a := 0; a < n; a++ {
			if row[a] == 0 {
				continue
			}
			jtr[a] += float64(row[a] * r[i])
			for b := a; b < n; b++ {
				jtj[a][b] += float64(row[a] * row[b])
			}
		}
	}
	for a := 0; a < n; a++ {
		for b := 0; b < a; b++ {
			jtj[a][b] = jtj[b][a]
		}
	}
	return jtj, jtr
}

// solveDamped solves the packed SPD system through linalg.SolveSPD; ok
// is false when the factorisation fails or the step is not finite.
func solveDamped(n int, packed, rhs []float64) ([]float64, bool) {
	s, err := linalg.NewSym(n, packed)
	if err != nil {
		return nil, false
	}
	v, err := linalg.SolveSPD(s, linalg.NewVec(rhs))
	if err != nil {
		return nil, false
	}
	step := v.Slice()
	for _, d := range step {
		if math.IsNaN(d) || math.IsInf(d, 0) {
			return nil, false
		}
	}
	return step, true
}

func validateLeastSquares(residual func([]float64) []float64, jacobian func([]float64) [][]float64,
	x0, lower, upper []float64, opts LeastSquaresOptions) error {
	n := len(x0)
	switch {
	case residual == nil || jacobian == nil:
		return invalid("residual and jacobian are required", map[string]any{"reason": "nil_callback"})
	case n == 0:
		return invalid("x0 is empty", map[string]any{"reason": "empty_x0"})
	case lower != nil && len(lower) != n:
		return invalid("lower must be nil or have one entry per parameter",
			map[string]any{"reason": "bounds_length", "want": n, "got": len(lower)})
	case upper != nil && len(upper) != n:
		return invalid("upper must be nil or have one entry per parameter",
			map[string]any{"reason": "bounds_length", "want": n, "got": len(upper)})
	case opts.MaxIter < 0:
		return invalid("MaxIter must be ≥ 0", map[string]any{"reason": "max_iter"})
	case opts.CostTol < 0 || math.IsNaN(opts.CostTol) || math.IsInf(opts.CostTol, 0):
		return invalid("CostTol must be a finite value ≥ 0", map[string]any{"reason": "cost_tol"})
	case opts.StepTol < 0 || math.IsNaN(opts.StepTol) || math.IsInf(opts.StepTol, 0):
		return invalid("StepTol must be a finite value ≥ 0", map[string]any{"reason": "step_tol"})
	}
	for i, v := range x0 {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return invalid("x0 must be finite", map[string]any{"reason": "non_finite_x0", "index": i})
		}
	}
	if lower != nil && upper != nil {
		for i := range x0 {
			if lower[i] > upper[i] {
				return invalid("a lower bound is above its upper bound",
					map[string]any{"reason": "bounds_crossed", "index": i})
			}
		}
	}
	return nil
}
