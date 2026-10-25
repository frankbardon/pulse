// Package optsolve solves one Compose `optimize` block: the block's
// rules (internal/optblock) compiled it against the runtime's bound
// models, and Solve runs the seeded multi-start SLSQP search
// (optimize.Minimize) over it and reads the optimum's figures — x*, f*,
// ∂f/∂xᵢ, the constraint multipliers in the objective's own direction,
// the active bounds and the starts that agree.
//
// Everything here is a pure function of the block, its resolved
// numbers and the bound models: the starts are a seeded Latin
// hypercube (internal/sweep's draw), the gradients central finite
// differences with every product barriered (float64(a*b)), the solver
// FMA-free — so the same block gives the same bits on every
// architecture, serial or not.
package optsolve

import (
	stderrors "errors"
	"math"
	"strconv"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/optimize"
	"github.com/frankbardon/pulse/types"
)

// cbrtEps is ε^(1/3) for float64 (ε = 2^-52): the central-difference
// step that balances truncation (O(h²)) against rounding (O(ε/h)).
const cbrtEps = 6.055454452393343e-06

// AgreeFactor scales the solver tolerance into the starts_agreeing
// window: a converged start agrees with the best when their objectives
// differ by at most AgreeFactor·tolerance·max(1, |f*|). SLSQP stops once
// a step moves the objective by less than tolerance, so two runs that
// reach the same optimum land within that order of each other; the
// factor absorbs their different approach paths, and max(1, |f*|) keeps
// the window meaningful on a large objective.
const AgreeFactor = 10

// Outcome is one block's solve: the result entry, and whether the block
// failed on its numbers (Result.Error set, every figure left zero).
type Outcome struct {
	Result types.OptimizeResult
	Failed bool
}

// Solve runs block i (spec, compiled by optblock.Compile against the
// runtime's bound models) from its Starts starting points — the Start
// point (the midpoint where none is set), then Starts − 1 points of a
// Latin hypercube inside the bounds drawn with seed — and reports the
// best converged run.
//
// No converged start fails the block alone: Outcome.Failed, the result
// carrying the best failed run's PULSE_OPTIMIZE_NOT_CONVERGED /
// _INFEASIBLE (fewest violations, then the best objective, then the
// earliest start). An expression that reads a Compose fit that failed
// on its data fails the block alone with that fit's code. Any other
// evaluation fault is fatal: the returned error, located by
// details.optimize and details.field.
func Solve(spec *types.OptimizeSpec, i int, c *optblock.Compiled, seed int64) (*Outcome, error) {
	s := &solver{spec: spec, index: i, c: c, sign: 1}
	if spec.Direction == types.OptimizeMaximize {
		s.sign = -1
	}
	acc := spec.Tolerance
	if acc == 0 {
		acc = 1e-6
	}
	opts := optimize.MinimizeOptions{Lower: c.Lower, Upper: c.Upper, Acc: acc, MaxIter: spec.MaxIterations}
	for k, prog := range c.Constraints {
		typ := optimize.Ineq
		if spec.Constraints[k].Type == types.OptimizeConstraintEq {
			typ = optimize.Eq
		}
		field := "optimize[" + strconv.Itoa(i) + "].constraints[" + strconv.Itoa(k) + "].expr"
		opts.Constraints = append(opts.Constraints, optimize.Constraint{
			Fn:   func(x []float64) float64 { return s.eval(prog, field, x) },
			Grad: func(x []float64) []float64 { return s.grad(prog, field, 1, x) },
			Type: typ,
		})
	}
	objField := "optimize[" + strconv.Itoa(i) + "].objective"
	f := func(x []float64) float64 { return float64(s.sign * s.eval(c.Objective, objField, x)) }
	g := func(x []float64) []float64 { return s.grad(c.Objective, objField, s.sign, x) }

	starts := startPoints(spec.EffectiveStarts(), c, seed)
	runs := make([]run, len(starts))
	for k, x0 := range starts {
		res, err, abort := s.minimize(f, g, x0, opts)
		if abort != nil {
			if se, ok := failedFit(abort); ok {
				// A read of a fit that failed on its data: no start can
				// evaluate the block, which fails alone with the fit's
				// own code.
				return &Outcome{Result: types.OptimizeResult{Name: spec.Name, Direction: spec.Direction, Error: se}, Failed: true}, nil
			}
			return nil, abort
		}
		runs[k] = run{res: res, err: err}
	}

	out := &Outcome{Result: types.OptimizeResult{Name: spec.Name, Direction: spec.Direction}}
	best := -1
	for k, r := range runs {
		if !r.converged() {
			continue
		}
		if best < 0 || r.res.F < runs[best].res.F {
			best = k
		}
	}
	if best < 0 {
		out.Failed = true
		out.Result.Error = failure(spec, i, runs)
		return out, nil
	}
	b := runs[best].res
	window := float64(float64(AgreeFactor*acc) * math.Max(1, math.Abs(b.F)))
	agreeing := 0
	for _, r := range runs {
		if r.converged() && math.Abs(r.res.F-b.F) <= window {
			agreeing++
		}
	}
	res := &out.Result
	if err := s.guard(func() { s.figures(res, b, objField) }); err != nil {
		return nil, err
	}
	res.Converged = true
	res.Status = b.Status.String()
	res.Iterations = b.Iterations
	res.MaxViolation = b.MaxViolation
	res.Starts = len(starts)
	res.StartsAgreeing = agreeing
	res.RNGSeed = seed
	return out, nil
}

// guard runs fn, turning an evaluation fault's unwind into its error.
func (s *solver) guard(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(abortSolve); !ok {
				panic(r)
			}
			err = s.fault
		}
	}()
	fn()
	return nil
}

// figures reads the optimum's figures off the best run b: f*, each
// variable's value, marginal ∂f/∂xᵢ and active bound, each constraint's
// value and multiplier.
func (s *solver) figures(res *types.OptimizeResult, b *optimize.MinimizeResult, objField string) {
	spec, c, i := s.spec, s.c, s.index
	res.Objective = s.eval(c.Objective, objField, b.X)
	marginals := s.grad(c.Objective, objField, 1, b.X)
	for j, name := range c.Names {
		v := types.OptimizeVariableResult{Name: name, Value: b.X[j], Marginal: marginals[j]}
		switch b.Active[j] {
		case optimize.AtLower:
			v.Active = types.OptimizeActiveLower
		case optimize.AtUpper:
			v.Active = types.OptimizeActiveUpper
		}
		res.Variables = append(res.Variables, v)
	}
	for k, cs := range spec.Constraints {
		field := "optimize[" + strconv.Itoa(i) + "].constraints[" + strconv.Itoa(k) + "].expr"
		res.Constraints = append(res.Constraints, types.OptimizeConstraintResult{
			Expr: cs.Expr, Type: cs.Type,
			Value: s.eval(c.Constraints[k], field, b.X),
			// SLSQP's multiplier is for the minimised objective (−f
			// under maximize): flip it into the objective's own
			// direction, ∇f = Σ μ ∇Expr.
			Multiplier: float64(s.sign * b.Multipliers[k]),
		})
	}
}

// run is one start's solve.
type run struct {
	res *optimize.MinimizeResult
	err error
}

// converged reports a start that stopped successfully on a finite
// objective.
func (r run) converged() bool {
	return r.err == nil && r.res != nil && !math.IsNaN(r.res.F) && !math.IsInf(r.res.F, 0)
}

// solver is one block's evaluation state. fault is the evaluation error
// that aborted the solve (a fatal fault, or a read of a failed fit).
type solver struct {
	spec  *types.OptimizeSpec
	index int
	c     *optblock.Compiled
	sign  float64
	fault error
}

// abortSolve unwinds optimize.Minimize from inside a callback: Minimize
// re-panics any value it does not own, and minimize recovers it.
type abortSolve struct{}

// minimize is optimize.Minimize with an evaluation fault surfaced: a
// fatal fault (or a failed-fit read) aborts the run and comes back as
// abort.
func (s *solver) minimize(f func([]float64) float64, g func([]float64) []float64, x0 []float64, opts optimize.MinimizeOptions) (res *optimize.MinimizeResult, err error, abort error) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(abortSolve); !ok {
				panic(r)
			}
			res, err, abort = nil, nil, s.fault
		}
	}()
	res, err = optimize.Minimize(f, g, x0, opts)
	if err != nil {
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || (ce.Code != errors.PULSE_OPTIMIZE_NOT_CONVERGED && ce.Code != errors.PULSE_OPTIMIZE_INFEASIBLE) {
			// PULSE_OPTIMIZE_INVALID: the rule set already admitted
			// every input, so this is an invariant violation.
			return nil, nil, s.locate(err, "optimize["+strconv.Itoa(s.index)+"]")
		}
	}
	return res, err, nil
}

// eval is prog at x as a float64. A fault records itself and aborts the
// solve (never returns into the solver with a made-up value).
func (s *solver) eval(prog *vm.Program, field string, x []float64) float64 {
	out, err := expr.Run(prog, s.c.Vars(x))
	if err != nil {
		s.fault = s.locate(err, field)
		panic(abortSolve{})
	}
	switch v := out.(type) {
	case float64:
		return v
	case nil:
		return math.NaN()
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	case float32:
		return float64(v)
	case uint:
		return float64(v)
	case uint64:
		return float64(v)
	}
	s.fault = errors.NewCodedErrorWithDetails(errors.PULSE_OPTIMIZE_EXPRESSION_INVALID,
		"optimize block "+strconv.Quote(s.spec.Name)+": expression at "+field+" yielded a non-number at an iterate",
		map[string]any{"optimize": s.index, "field": field, "reason": optblock.ReasonNotNumeric})
	panic(abortSolve{})
}

// locate is an evaluation error located at the block: a coded error
// keeps its code, message and details and gains details.optimize /
// details.field; any other is PROCESSING_RUNTIME.
func (s *solver) locate(err error, field string) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
			"optimize block "+strconv.Quote(s.spec.Name)+": "+err.Error(),
			map[string]any{"optimize": s.index, "field": field})
	}
	d := make(map[string]any, len(ce.Details)+2)
	for k, v := range ce.Details {
		d[k] = v
	}
	d["optimize"] = s.index
	d["field"] = field
	out := errors.NewCodedErrorWithDetails(ce.Code, "optimize block "+strconv.Quote(s.spec.Name)+": "+ce.Message, d)
	out.Cause = ce.Cause
	return out
}

// grad is sign·∇prog at x by finite differences: per variable a step
// h = cbrt(ε)·max(|xᵢ|, min(1, upperᵢ − lowerᵢ)) (1 when that is 0),
// at most a quarter of the bound width; the central stencil
// (f(x+h) − f(x−h)) / 2h when it stays inside the bounds (or the
// variable is fixed), else the one-sided second-order stencil toward
// the interior. Every step is the representable difference of the two
// points, so the quotient divides by the step actually taken.
func (s *solver) grad(prog *vm.Program, field string, sign float64, x []float64) []float64 {
	n := len(x)
	out := make([]float64, n)
	pt := make([]float64, n)
	copy(pt, x)
	at := func(j int, v float64) float64 {
		pt[j] = v
		r := s.eval(prog, field, pt)
		pt[j] = x[j]
		return r
	}
	var f0 float64
	haveF0 := false
	for j := 0; j < n; j++ {
		xj, lo, hi := x[j], s.c.Lower[j], s.c.Upper[j]
		w := hi - lo
		scale := math.Max(math.Abs(xj), math.Min(1, w))
		if scale == 0 {
			scale = 1
		}
		h := float64(cbrtEps * scale)
		if w > 0 && h > w/4 {
			h = w / 4
		}
		var d float64
		switch {
		case w == 0 || (xj-h >= lo && xj+h <= hi):
			xp, xm := xj+h, xj-h
			d = (at(j, xp) - at(j, xm)) / (xp - xm)
		default:
			if !haveF0 {
				f0, haveF0 = s.eval(prog, field, x), true
			}
			dir := 1.0
			if xj+2*h > hi {
				dir = -1
			}
			x1 := xj + float64(dir*h)
			step := x1 - xj
			x2 := xj + float64(2*step)
			// (−3f(x) + 4f(x+h) − f(x+2h)) / 2h, h signed.
			num := float64(-3*f0) + float64(4*at(j, x1)) - at(j, x2)
			d = num / float64(2*step)
		}
		out[j] = float64(sign * d)
	}
	return out
}

// startPoints are the block's starting points: the compiled Start (the
// midpoint where it is NaN), then n − 1 Latin-hypercube points mapped
// into the bounds, lower + u·(upper − lower), clamped against the final
// rounding.
func startPoints(n int, c *optblock.Compiled, seed int64) [][]float64 {
	dims := len(c.Names)
	out := make([][]float64, 0, n)
	first := make([]float64, dims)
	for j := range first {
		lo, hi := c.Lower[j], c.Upper[j]
		first[j] = c.Start[j]
		if math.IsNaN(first[j]) {
			first[j] = lo + float64(0.5*(hi-lo))
		}
	}
	out = append(out, first)
	if n <= 1 {
		return out
	}
	u := sweep.LatinHypercube(seed, n-1, dims)
	for k := 0; k < n-1; k++ {
		x := make([]float64, dims)
		for j := range x {
			lo, hi := c.Lower[j], c.Upper[j]
			v := lo + float64(u[j][k]*(hi-lo))
			x[j] = math.Min(math.Max(v, lo), hi)
		}
		out = append(out, x)
	}
	return out
}

// failure is the block's error when no start converged: the best failed
// run's solver error — fewest violations, then the best objective, then
// the earliest start — with the block located and the starts counted.
func failure(spec *types.OptimizeSpec, i int, runs []run) *types.SlotError {
	pick := -1
	for k, r := range runs {
		if r.res == nil {
			continue
		}
		if pick < 0 || better(r.res, runs[pick].res) {
			pick = k
		}
	}
	var ce *errors.CodedError
	code, msg := errors.PULSE_OPTIMIZE_NOT_CONVERGED, "no start converged"
	details := map[string]any{}
	if pick >= 0 && stderrors.As(runs[pick].err, &ce) {
		code, msg = ce.Code, ce.Message
		for k, v := range ce.Details {
			details[k] = v
		}
	}
	details["optimize"] = i
	details["starts"] = len(runs)
	if pick >= 0 {
		details["start"] = pick
	}
	return &types.SlotError{
		Code:    string(code),
		Message: "optimize block " + strconv.Quote(spec.Name) + ": no start of " + strconv.Itoa(len(runs)) + " converged; the best stopped with: " + msg,
		Details: details,
	}
}

// better ranks a failed run above b: a smaller constraint violation,
// then a smaller (minimised) objective; NaN ranks last.
func better(a, b *optimize.MinimizeResult) bool {
	if a.MaxViolation != b.MaxViolation {
		return less(a.MaxViolation, b.MaxViolation)
	}
	return less(a.F, b.F)
}

func less(a, b float64) bool {
	switch {
	case math.IsNaN(a):
		return false
	case math.IsNaN(b):
		return true
	}
	return a < b
}

// failedFit is err as the block's per-slot error when it is a read of
// a Compose fit that failed on its data (model.FailedFitRefusal, located
// at the block): its own code, message and details.
func failedFit(err error) (*types.SlotError, bool) {
	var ce *errors.CodedError
	if !stderrors.Is(err, model.ErrFitFailed) || !stderrors.As(err, &ce) {
		return nil, false
	}
	se := &types.SlotError{Code: string(ce.Code), Message: ce.Message}
	if len(ce.Details) > 0 {
		se.Details = make(map[string]any, len(ce.Details))
		for k, v := range ce.Details {
			se.Details[k] = v
		}
	}
	return se, true
}
