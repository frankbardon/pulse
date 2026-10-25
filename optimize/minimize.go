package optimize

import (
	"math"

	perr "github.com/frankbardon/pulse/errors"
)

// ConstraintType says whether a Constraint holds with equality or as a
// lower bound of zero. The zero value is invalid, so a Constraint that
// forgets its Type is refused rather than silently read as one kind.
type ConstraintType int

const (
	// Eq is an equality constraint: Fn(x) = 0.
	Eq ConstraintType = iota + 1
	// Ineq is an inequality constraint: Fn(x) ≥ 0.
	Ineq
)

// String returns "eq" or "ineq".
func (t ConstraintType) String() string {
	switch t {
	case Eq:
		return "eq"
	case Ineq:
		return "ineq"
	}
	return "invalid"
}

// Constraint is one general constraint on the decision vector: Fn(x) = 0
// (Eq) or Fn(x) ≥ 0 (Ineq), with Grad returning ∂Fn/∂x (length len(x)).
type Constraint struct {
	Fn   func(x []float64) float64
	Grad func(x []float64) []float64
	Type ConstraintType
}

// MinimizeOptions configures Minimize. The zero value is an unbounded,
// unconstrained problem with SciPy's defaults.
type MinimizeOptions struct {
	// Lower and Upper are per-variable bounds: nil, or exactly len(x0)
	// entries. An infinite or NaN entry marks that side unbounded; a
	// finite Lower[i] above Upper[i] is refused.
	Lower, Upper []float64
	// Constraints are the general constraints, in any mix of Eq and
	// Ineq; MinimizeResult.Multipliers follows this order.
	Constraints []Constraint
	// Acc is SLSQP's accuracy goal (SciPy's ftol): the stop on the
	// objective change, step length and constraint violation. 0 means
	// 1e-6; a negative or non-finite value is refused.
	Acc float64
	// MaxIter caps the major iterations. 0 means 100; negative is refused.
	MaxIter int
}

// ActiveBound reports which of a variable's bounds holds at the solution.
type ActiveBound int8

const (
	// NotActive: the variable sits strictly inside its bounds (or has none).
	NotActive ActiveBound = iota
	// AtLower: the variable sits on its lower bound (also reported for a
	// variable fixed by Lower == Upper).
	AtLower
	// AtUpper: the variable sits on its upper bound.
	AtUpper
)

// Status is SLSQP's exit mode, numbered as SciPy numbers it.
type Status int

const (
	StatusSuccess           Status = 0 // optimization terminated successfully
	StatusTooManyEquality   Status = 2 // more equality constraints than variables
	StatusLSQIterLimit      Status = 3 // more than 3·n iterations in the LSQ subproblem
	StatusIncompatible      Status = 4 // inequality constraints incompatible
	StatusSingularE         Status = 5 // singular matrix E in the LSQ subproblem
	StatusSingularC         Status = 6 // singular matrix C in the LSQ subproblem
	StatusRankDeficient     Status = 7 // rank-deficient equality constraint subproblem
	StatusPositiveDirection Status = 8 // positive directional derivative in the line search
	StatusIterLimit         Status = 9 // iteration limit reached
)

var statusMessages = map[Status]string{
	StatusSuccess:           "Optimization terminated successfully",
	StatusTooManyEquality:   "More equality constraints than independent variables",
	StatusLSQIterLimit:      "More than 3*n iterations in LSQ subproblem",
	StatusIncompatible:      "Inequality constraints incompatible",
	StatusSingularE:         "Singular matrix E in LSQ subproblem",
	StatusSingularC:         "Singular matrix C in LSQ subproblem",
	StatusRankDeficient:     "Rank-deficient equality constraint subproblem HFTI",
	StatusPositiveDirection: "Positive directional derivative for linesearch",
	StatusIterLimit:         "Iteration limit reached",
}

// String returns SciPy's message for the exit mode.
func (s Status) String() string {
	if m, ok := statusMessages[s]; ok {
		return m
	}
	return "Unknown exit mode"
}

// MinimizeResult is the solver's final iterate.
type MinimizeResult struct {
	// X is the final decision vector; F is the objective there.
	X []float64
	F float64
	// Multipliers holds one Lagrange multiplier per Constraint, in
	// MinimizeOptions.Constraints order, from the last QP subproblem. At
	// a solution ∇f = Σ Multipliers[i]·∇Fn_i on the free variables, so
	// a multiplier is the rate the optimal objective moves as its
	// constraint's right-hand side is relaxed; an Ineq multiplier is ≥ 0
	// when its constraint binds and 0 when it is slack.
	Multipliers []float64
	// Active marks, per variable, the bound it sits on: within
	// sqrt(machine epsilon)·(1+|bound|) of it.
	Active []ActiveBound
	// MaxViolation is the largest constraint violation at X: |Fn| for an
	// Eq constraint, max(−Fn, 0) for an Ineq one (0 with no constraints).
	MaxViolation float64
	Status       Status
	Iterations   int
	FuncEvals    int
	GradEvals    int
}

// badEval carries a mid-run callback fault (a gradient of the wrong
// length) out of the solver loop to Minimize, which turns it into a
// coded error; it never escapes the package.
type badEval struct{ err *perr.CodedError }

// Minimize minimises f, with gradient grad, from x0 by Sequential Least
// Squares Programming (Kraft's SLSQP, as SciPy 1.18.1 ships it) subject
// to opts' bounds and general constraints. The arithmetic is FMA-free,
// so a problem whose callbacks are themselves deterministic gives the
// same bits on every architecture.
//
// x0 is clipped to the bounds; an infeasible start for the general
// constraints is allowed. Bad input (empty or non-finite x0, nil
// callbacks, a bound or option out of range, a constraint with no Type,
// more equality constraints than variables, a gradient of the wrong
// length) is a PULSE_OPTIMIZE_INVALID error with a nil result. A run
// that stops short returns its final iterate WITH an error:
// PULSE_OPTIMIZE_INFEASIBLE when the constraints could not be met
// (StatusIncompatible, StatusSingularC, or any other early stop with a
// constraint violation above 10·Acc), else PULSE_OPTIMIZE_NOT_CONVERGED.
// A StatusSuccess result has a nil error.
func Minimize(f func(x []float64) float64, grad func(x []float64) []float64, x0 []float64, opts MinimizeOptions) (res *MinimizeResult, err error) {
	n := len(x0)
	if err := validateMinimize(f, grad, x0, opts); err != nil {
		return nil, err
	}
	acc := opts.Acc
	if acc == 0 {
		acc = 1e-6
	}
	itermax := opts.MaxIter
	if itermax == 0 {
		itermax = 100
	}
	xl, xu := boundVector(opts.Lower, n), boundVector(opts.Upper, n)

	// The solver wants equalities first; order maps solver row → caller
	// index.
	var order []int
	for i, c := range opts.Constraints {
		if c.Type == Eq {
			order = append(order, i)
		}
	}
	meq := len(order)
	for i, c := range opts.Constraints {
		if c.Type == Ineq {
			order = append(order, i)
		}
	}
	m := len(order)

	// Every callback sees a scratch copy of the iterate, so a caller that
	// writes into x cannot corrupt the solver's state.
	xs := make([]float64, n)
	call := func(x []float64) []float64 { copy(xs, x); return xs }
	fill := func(dst, g []float64, what string, idx int) {
		if len(g) != n {
			details := map[string]any{"reason": "gradient_length", "want": n, "got": len(g)}
			if idx >= 0 {
				details["constraint"] = idx
			}
			panic(badEval{perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_INVALID,
				what+" returned a gradient of the wrong length", details)})
		}
		copy(dst, g)
	}
	p := problem{
		n: n, m: m, meq: meq,
		f:    func(x []float64) float64 { return f(call(x)) },
		grad: func(x, g []float64) { fill(g, grad(call(x)), "grad", -1) },
		cons: func(x, d []float64) {
			for r, ci := range order {
				d[r] = opts.Constraints[ci].Fn(call(x))
			}
		},
		jac: func(x, cm []float64) {
			row := make([]float64, n)
			for r, ci := range order {
				fill(row, opts.Constraints[ci].Grad(call(x)), "constraint Grad", ci)
				for j := 0; j < n; j++ {
					cm[r+j*m] = row[j]
				}
			}
		},
	}

	defer func() {
		if r := recover(); r != nil {
			be, ok := r.(badEval)
			if !ok {
				panic(r)
			}
			res, err = nil, be.err
		}
	}()
	out := slsqp(p, x0, xl, xu, acc, itermax)

	res = &MinimizeResult{
		X:           out.x,
		F:           out.fun,
		Multipliers: make([]float64, len(opts.Constraints)),
		Active:      make([]ActiveBound, n),
		Status:      Status(out.mode),
		Iterations:  out.nit,
		FuncEvals:   out.nfev,
		GradEvals:   out.njev,
	}
	for r, ci := range order {
		res.Multipliers[ci] = out.mult[r]
	}
	tolB := math.Sqrt(2.220446049250313e-16)
	for i, xi := range res.X {
		switch {
		case !math.IsNaN(xl[i]) && math.Abs(xi-xl[i]) <= float64(tolB*(1+math.Abs(xl[i]))):
			res.Active[i] = AtLower
		case !math.IsNaN(xu[i]) && math.Abs(xi-xu[i]) <= float64(tolB*(1+math.Abs(xu[i]))):
			res.Active[i] = AtUpper
		}
	}
	for _, c := range opts.Constraints {
		v := c.Fn(call(res.X))
		if c.Type == Eq {
			v = math.Abs(v)
		} else {
			v = math.Max(-v, 0)
		}
		if v > res.MaxViolation || math.IsNaN(v) {
			res.MaxViolation = v
		}
	}
	if res.Status == StatusSuccess {
		return res, nil
	}
	details := map[string]any{
		"status":        int(res.Status),
		"message":       res.Status.String(),
		"iterations":    res.Iterations,
		"max_violation": res.MaxViolation,
	}
	switch {
	case res.Status == StatusIncompatible || res.Status == StatusSingularC ||
		!(res.MaxViolation <= float64(10*math.Abs(acc))):
		return res, perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_INFEASIBLE,
			"the constraints could not be satisfied: "+res.Status.String(), details)
	default:
		return res, perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_NOT_CONVERGED,
			"the solver stopped before converging: "+res.Status.String(), details)
	}
}

// boundVector turns a caller bound slice into the solver's form: NaN for
// an absent (nil, infinite or NaN) bound.
func boundVector(b []float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = math.NaN()
		if b != nil && !math.IsInf(b[i], 0) {
			out[i] = b[i]
		}
	}
	return out
}

func invalid(msg string, details map[string]any) error {
	return perr.NewCodedErrorWithDetails(perr.PULSE_OPTIMIZE_INVALID, msg, details)
}

func validateMinimize(f func([]float64) float64, grad func([]float64) []float64, x0 []float64, opts MinimizeOptions) error {
	n := len(x0)
	switch {
	case f == nil || grad == nil:
		return invalid("f and grad are required", map[string]any{"reason": "nil_callback"})
	case n == 0:
		return invalid("x0 is empty", map[string]any{"reason": "empty_x0"})
	case opts.Lower != nil && len(opts.Lower) != n:
		return invalid("Lower must be nil or have one entry per variable",
			map[string]any{"reason": "bounds_length", "want": n, "got": len(opts.Lower)})
	case opts.Upper != nil && len(opts.Upper) != n:
		return invalid("Upper must be nil or have one entry per variable",
			map[string]any{"reason": "bounds_length", "want": n, "got": len(opts.Upper)})
	case opts.Acc < 0 || math.IsNaN(opts.Acc) || math.IsInf(opts.Acc, 0):
		return invalid("Acc must be a finite value ≥ 0", map[string]any{"reason": "acc"})
	case opts.MaxIter < 0:
		return invalid("MaxIter must be ≥ 0", map[string]any{"reason": "max_iter"})
	}
	for i, v := range x0 {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return invalid("x0 must be finite", map[string]any{"reason": "non_finite_x0", "index": i})
		}
	}
	if opts.Lower != nil && opts.Upper != nil {
		for i := range x0 {
			if opts.Lower[i] > opts.Upper[i] {
				return invalid("a lower bound is above its upper bound",
					map[string]any{"reason": "bounds_crossed", "index": i})
			}
		}
	}
	neq := 0
	for i, c := range opts.Constraints {
		if c.Fn == nil || c.Grad == nil {
			return invalid("a constraint needs Fn and Grad",
				map[string]any{"reason": "nil_callback", "constraint": i})
		}
		switch c.Type {
		case Eq:
			neq++
		case Ineq:
		default:
			return invalid("a constraint's Type must be Eq or Ineq",
				map[string]any{"reason": "constraint_type", "constraint": i})
		}
	}
	if neq > n {
		return invalid("more equality constraints than variables",
			map[string]any{"reason": "too_many_equalities", "equalities": neq, "variables": n})
	}
	return nil
}
