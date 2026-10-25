package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// OptimizeSpec is one entry of ComposedRequest.Optimize: a named
// constrained optimisation over decision variables, solved after the
// batch's `fits` with no record data. The objective and every
// constraint are expressions over the decision variables (each bound as
// a float64 identifier, evaluated per iterate), the batch's `params`
// (params.<name>, folded at compile), coef() over the batch's `fits`
// and the expression built-ins (hill(), exp(), log(), …).
//
// Each block is solved from Starts starting points — the variables'
// Start point, then seeded Latin-hypercube points inside the bounds —
// and the best run is reported in ComposedResponse.Optimizations, in
// declaration order. Predict validates every block without solving it:
// the expressions compile against the identifiers they may read, every
// all-literal coef() call resolves against the fits, the variable names
// are unique and every bound is ordered.
//
// It rides as the optional `optimize` slot on ComposedRequest, gated by
// capability:compose_optimize. A nil / empty list is absent: the slot is
// `omitempty`, so an optimize-free request is byte-identical on the wire
// and under CanonicalHash.
type OptimizeSpec struct {
	// Name identifies the block; required and unique among the blocks.
	// Its result echoes it.
	Name string `json:"name"`

	// Variables are the decision variables, at least one, in the order
	// the result reports them. Every name is an identifier unique within
	// the block.
	Variables []OptimizeVariable `json:"variables"`

	// Objective is the expression maximised or minimised (Direction).
	// It must yield a number.
	Objective string `json:"objective"`

	// Direction is maximize or minimize (OptimizeDirection*). Required:
	// no default, so a block never silently optimises the wrong way.
	Direction OptimizeDirection `json:"direction"`

	// Constraints are the general constraints, in any mix of eq
	// (Expr = 0) and ineq (Expr >= 0). There may be no more eq
	// constraints than Variables.
	Constraints []OptimizeConstraint `json:"constraints,omitempty"`

	// Starts is the number of starting points solved: the variables'
	// Start point first, then seeded Latin-hypercube points inside the
	// bounds. Absent (nil) is OptimizeDefaultStarts; set, it is 1 to
	// OptimizeMaxStarts.
	Starts *int `json:"starts,omitempty"`

	// RNGSeed seeds the Latin-hypercube starts. Zero derives the seed
	// from the block's wire form with rng_seed excluded (never the
	// clock), so the same block always draws the same starts; the
	// effective seed is echoed on the result and on predict's
	// OptimizeSummary.
	RNGSeed int64 `json:"rng_seed,omitempty"`

	// Tolerance is the solver's accuracy goal (the stop on the objective
	// change, step length and constraint violation). Zero is the solver
	// default 1e-6; negative is refused.
	Tolerance float64 `json:"tolerance,omitempty"`

	// MaxIterations caps each start's major iterations. Zero is the
	// solver default 100; negative is refused.
	MaxIterations int `json:"max_iterations,omitempty"`

	// Models loads saved model files (a regression `save`) by name for
	// this block's coef() calls — the Request.Models contract (ModelRef):
	// each name joins the block's model namespace above the batch's
	// `fits`, so coef() resolves it exactly like a fit of the same name,
	// and a name a fit (or an earlier entry) already binds is
	// PULSE_MODEL_NAME_COLLISION. Every file is read and decoded before
	// any fit or slot runs, by predict and the runtime alike, with the
	// model-file codes (PULSE_MODEL_REF_INVALID, _FILE_UNREADABLE,
	// _FILE_INVALID, _VERSION_UNSUPPORTED) located by details.optimize
	// and details.field. Gated by capability:models.
	Models []ModelRef `json:"models,omitempty"`
}

// OptimizeDefaultStarts is the starting-point count of a block that sets
// no `starts`.
const OptimizeDefaultStarts = 8

// OptimizeMaxStarts is the most starting points a block may set.
const OptimizeMaxStarts = 1000

// EffectiveStarts is the number of starting points the block solves:
// Starts when set, else OptimizeDefaultStarts.
func (s *OptimizeSpec) EffectiveStarts() int {
	if s == nil || s.Starts == nil {
		return OptimizeDefaultStarts
	}
	return *s.Starts
}

// OptimizeVariable is one decision variable: an identifier the
// expressions read, its bounds and an optional starting value.
type OptimizeVariable struct {
	// Name is the identifier the objective and constraints read
	// (`[A-Za-z_][A-Za-z0-9_]*`), unique within the block. It may not be
	// `params` or the name of an expression function.
	Name string `json:"name"`

	// Lower and Upper bound the variable, Lower <= Upper (equal fixes
	// it). Both are required: a number, or a reference to a numeric
	// batch param.
	Lower OptimizeBound `json:"lower"`
	Upper OptimizeBound `json:"upper"`

	// Start is the first starting point's value, inside the bounds.
	// Absent starts at the midpoint of the bounds.
	Start OptimizeBound `json:"start,omitzero"`
}

// OptimizeBound is a decision variable's bound or start value: a number
// (BoundValue), or a reference to a numeric param of the batch
// (BoundParam) — on the wire a JSON number or the string
// "params.<name>". The param is resolved before anything is solved, by
// predict and the runtime alike; it must name a finite number. The zero
// value is absent.
type OptimizeBound struct {
	value float64
	param string
	set   bool
}

// BoundValue returns the literal bound v.
func BoundValue(v float64) OptimizeBound { return OptimizeBound{value: v, set: true} }

// BoundParam returns the bound read from the batch param name (the wire
// form "params.<name>").
func BoundParam(name string) OptimizeBound { return OptimizeBound{param: name, set: true} }

// IsZero reports whether the bound is absent. It is what `omitzero`
// consults.
func (b OptimizeBound) IsZero() bool { return !b.set }

// IsParam reports whether the bound is a param reference.
func (b OptimizeBound) IsParam() bool { return b.set && b.param != "" }

// Param returns the referenced param's name ("" for a literal or an
// absent bound).
func (b OptimizeBound) Param() string { return b.param }

// Value returns the literal bound (0 for a param reference or an absent
// bound).
func (b OptimizeBound) Value() float64 {
	if b.param != "" {
		return 0
	}
	return b.value
}

// optimizeBoundParamPrefix prefixes a param-reference bound on the wire.
const optimizeBoundParamPrefix = "params."

// MarshalJSON writes the number, the "params.<name>" string, or null
// for an absent bound.
func (b OptimizeBound) MarshalJSON() ([]byte, error) {
	switch {
	case !b.set:
		return []byte("null"), nil
	case b.param != "":
		return json.Marshal(optimizeBoundParamPrefix + b.param)
	}
	if math.IsNaN(b.value) || math.IsInf(b.value, 0) {
		return []byte("null"), nil
	}
	return json.Marshal(b.value)
}

// UnmarshalJSON accepts a number or a "params.<name>" string; null
// leaves the bound absent. A string that is not a params reference, or
// any other JSON shape, is an error.
func (b *OptimizeBound) UnmarshalJSON(raw []byte) error {
	trimmed := bytes.TrimSpace(raw)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
		*b = OptimizeBound{}
		return nil
	case len(trimmed) > 0 && trimmed[0] == '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		name, ok := bytes.CutPrefix([]byte(s), []byte(optimizeBoundParamPrefix))
		if !ok || len(name) == 0 {
			return fmt.Errorf("optimize bound %q: want a number or \"params.<name>\"", s)
		}
		*b = BoundParam(string(name))
		return nil
	default:
		var v float64
		if err := json.Unmarshal(trimmed, &v); err != nil {
			return fmt.Errorf("optimize bound: want a number or \"params.<name>\": %w", err)
		}
		*b = BoundValue(v)
		return nil
	}
}

// OptimizeDirection says whether a block maximises or minimises its
// objective.
type OptimizeDirection string

// The optimize directions.
const (
	OptimizeMaximize OptimizeDirection = "maximize"
	OptimizeMinimize OptimizeDirection = "minimize"
)

// AllOptimizeDirections returns every optimize direction.
func AllOptimizeDirections() []OptimizeDirection {
	return []OptimizeDirection{OptimizeMaximize, OptimizeMinimize}
}

// OptimizeConstraint is one general constraint of a block: Expr = 0
// (type eq) or Expr >= 0 (type ineq), Expr an expression over the same
// identifiers as the objective that yields a number.
type OptimizeConstraint struct {
	// Expr is the constraint expression.
	Expr string `json:"expr"`
	// Type is eq or ineq (OptimizeConstraint*). Required.
	Type OptimizeConstraintType `json:"type"`
}

// OptimizeConstraintType is a constraint's kind.
type OptimizeConstraintType string

// The constraint types.
const (
	// OptimizeConstraintEq holds with equality: Expr = 0.
	OptimizeConstraintEq OptimizeConstraintType = "eq"
	// OptimizeConstraintIneq holds as a lower bound of zero: Expr >= 0.
	OptimizeConstraintIneq OptimizeConstraintType = "ineq"
)

// AllOptimizeConstraintTypes returns every constraint type.
func AllOptimizeConstraintTypes() []OptimizeConstraintType {
	return []OptimizeConstraintType{OptimizeConstraintEq, OptimizeConstraintIneq}
}

// OptimizeResult is one ComposedResponse.Optimizations entry: a block's
// best run over its starts, at full precision. Like `fits` it sits
// outside the Compose `return` — never rounded.
//
// Each start is solved by SLSQP (optimize.Minimize) on the objective —
// negated under maximize — with central finite-difference gradients
// (step cbrt(ε)·max(|xᵢ|, min(1, upperᵢ − lowerᵢ)), at most a quarter
// of the bound width; a stencil that would leave the bounds turns
// one-sided, second order). The best run is the converged start with
// the best objective, the earliest on a tie.
//
// A block that failed on its numbers (no start converged, or the
// constraints could not be met) keeps its entry: Name, Direction and
// the error, every figure absent on the wire (see SlotError).
type OptimizeResult struct {
	// Name and Direction echo the block.
	Name      string            `json:"name"`
	Direction OptimizeDirection `json:"direction"`

	// Objective is the objective at the optimum (f*).
	Objective float64 `json:"objective"`

	// Variables are the decision variables at the optimum, in the
	// block's order.
	Variables []OptimizeVariableResult `json:"variables"`

	// Constraints are the block's constraints at the optimum, in the
	// block's order (absent without constraints).
	Constraints []OptimizeConstraintResult `json:"constraints,omitempty"`

	// Converged reports the best run's solver stop was a success;
	// Status is the solver's exit message and Iterations its major
	// iterations.
	Converged  bool   `json:"converged"`
	Status     string `json:"status"`
	Iterations int    `json:"iterations"`

	// MaxViolation is the largest constraint violation at the optimum:
	// |Expr| for an eq constraint, max(-Expr, 0) for an ineq one (0
	// without constraints).
	MaxViolation float64 `json:"max_violation"`

	// Starts is the number of starting points solved; StartsAgreeing
	// how many of them converged to the best objective f* within
	// 10·tolerance·max(1, |f*|) — the solver stops once a step moves
	// the objective by less than tolerance, so two runs reaching the
	// same optimum agree to that order — the best run included. Fewer
	// agreeing starts than starts flags a multi-modal objective.
	Starts         int `json:"starts"`
	StartsAgreeing int `json:"starts_agreeing"`

	// RNGSeed is the effective seed the Latin-hypercube starts were
	// drawn with: the block's rng_seed when set, else the one derived
	// from the block. Pin it to replay the run.
	RNGSeed int64 `json:"rng_seed"`

	// Error is set when the block failed on its numbers: the entry keeps
	// Name, Direction and the error, every other figure absent on the
	// wire (see SlotError).
	Error *SlotError `json:"error,omitempty"`
}

// OptimizeVariableResult is one decision variable at the optimum.
type OptimizeVariableResult struct {
	// Name is the variable's name.
	Name string `json:"name"`

	// Value is the variable at the optimum (x*).
	Value float64 `json:"value"`

	// Marginal is the objective's partial derivative in the variable at
	// the optimum (∂f/∂xᵢ): the objective's change per unit of the
	// variable — a marginal return. On a variable not at a bound it
	// equals Σⱼ multiplierⱼ·∂Exprⱼ/∂xᵢ over the constraints.
	Marginal float64 `json:"marginal"`

	// Active names the bound the variable sits on at the optimum —
	// lower or upper — absent when it is strictly inside its bounds.
	Active OptimizeActiveBound `json:"active,omitempty"`
}

// OptimizeActiveBound names the bound a variable sits on at the optimum.
type OptimizeActiveBound string

// The active bounds.
const (
	OptimizeActiveLower OptimizeActiveBound = "lower"
	OptimizeActiveUpper OptimizeActiveBound = "upper"
)

// AllOptimizeActiveBounds returns every active-bound value.
func AllOptimizeActiveBounds() []OptimizeActiveBound {
	return []OptimizeActiveBound{OptimizeActiveLower, OptimizeActiveUpper}
}

// OptimizeConstraintResult is one constraint at the optimum.
type OptimizeConstraintResult struct {
	// Expr and Type echo the constraint.
	Expr string                 `json:"expr"`
	Type OptimizeConstraintType `json:"type"`

	// Value is the constraint expression at the optimum.
	Value float64 `json:"value"`

	// Multiplier is the constraint's Lagrange multiplier in the
	// objective's own direction: at the optimum ∇objective = Σ
	// multiplierⱼ·∇Exprⱼ on the variables not at a bound, for maximize
	// and minimize alike. It is the constraint's shadow price: adding a
	// constant k to Expr moves the optimal objective at the rate
	// −multiplier per unit of k — so for `x1 + x2 - params.budget` (eq)
	// it is the objective gained per extra unit of budget, and equals
	// every free variable's Marginal. An ineq multiplier is 0 when its
	// constraint is slack; binding, it is ≥ 0 under minimize and ≤ 0
	// under maximize.
	Multiplier float64 `json:"multiplier"`
}

// slotFailureOptimize is the wire form of an OptimizeResult carrying an
// Error: its identity and the error, no figure.
type slotFailureOptimize struct {
	Name      string            `json:"name"`
	Direction OptimizeDirection `json:"direction"`
	Error     *SlotError        `json:"error"`
}

// MarshalJSON writes the result with every non-finite float as null;
// see MarshalFinite. A failed block writes {name, direction, error}
// only.
func (r OptimizeResult) MarshalJSON() ([]byte, error) {
	if r.Error != nil {
		return MarshalFinite(slotFailureOptimize{Name: r.Name, Direction: r.Direction, Error: r.Error})
	}
	type alias OptimizeResult
	return MarshalFinite((*alias)(&r))
}
