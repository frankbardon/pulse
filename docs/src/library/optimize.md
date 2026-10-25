# Numerical Optimization (`optimize`)

`github.com/frankbardon/pulse/optimize` holds Pulse's numerical solvers. Pulse's own estimators use it, and embedders can import it directly. It imports only the standard library and Pulse's `errors`, `linalg` and deterministic-math packages.

```go
import "github.com/frankbardon/pulse/optimize"
```

## `LeastSquares`: bounded nonlinear least squares

`LeastSquares` minimizes `½·Σ rᵢ(x)²` by Levenberg–Marquardt. You supply the residuals and their Jacobian (one row per residual), a start, and optional per-parameter bounds.

```go
// fit y ≈ a·exp(−k·t) with a, k ≥ 0
res, err := optimize.LeastSquares(
	func(x []float64) []float64 { /* rᵢ = a·exp(−k·tᵢ) − yᵢ */ },
	func(x []float64) [][]float64 { /* ∂rᵢ/∂a, ∂rᵢ/∂k per row */ },
	[]float64{1, 1},   // start
	[]float64{0, 0},   // lower (nil = none)
	nil,               // upper
	optimize.LeastSquaresOptions{},
)
```

Bounds are held by projection: a step that would cross a bound lands exactly on it, and a parameter whose gradient pushes past its bound is held there. A coefficient bounded at 0 can therefore be exactly 0. Nothing is reparametrized through `exp`. A trial point whose cost rises, is not finite, or has a non-finite Jacobian is rejected and the damping grows.

The schedule is fixed. Damping is `λ·max(diag(JᵀJ), 1e-12)`, with `λ` starting at `1e-3`, divided by 3 on an accepted step (floor `1e-12`) and multiplied by 4 on a rejected one. Each damped system is solved with `linalg.SolveSPD`.

| Option | Meaning | Default |
|---|---|---|
| `MaxIter` | outer-iteration cap (one per accepted step) | `2000` |
| `CostTol` | stop when an accepted step lowers the cost by at most `CostTol·cost` | `1e-13` |
| `StepTol` | stop when an accepted step moves every parameter by at most `StepTol·(1+\|xᵢ\|)` | `1e-10` |

The run also stops, converged, when no step lowers the cost at any damping up to `1e16`. The result carries:

- `X` and `Cost` (`½·Σ rᵢ²` at `X`).
- `JTJ`: `JᵀJ` at `X` as a `*linalg.Sym`, over every parameter. For the Gauss–Newton covariance `σ²·(JᵀJ)⁻¹`, take `σ² = 2·Cost/(m − n)`, drop the rows and columns of parameters held on a bound, and invert with `linalg.InverseSPD`.
- `Active`: per parameter, `AtLower`, `AtUpper` or `NotActive`. Here it means sitting exactly on the bound.
- `Status` (`LSConvergedCost`, `LSConvergedStep`, `LSConvergedDamping` or `LSIterLimit`; `Status.Converged()`), `Iterations`, `FuncEvals`, `JacEvals`.

## `Minimize`: constrained minimization (SLSQP)

`Minimize` minimizes a smooth objective under simple bounds and any number of general constraints. It is SLSQP (Sequential Least Squares Programming), ported statement by statement from SciPy 1.18.1. The SciPy BSD-3 notice ships beside the code in `optimize/SCIPY_LICENSE.txt`. You supply the gradients.

```go
// min x² + y²  subject to  x + y = 1,  0 ≤ x ≤ 10
f := func(x []float64) float64 { return float64(x[0]*x[0]) + float64(x[1]*x[1]) }
grad := func(x []float64) []float64 { return []float64{2 * x[0], 2 * x[1]} }

res, err := optimize.Minimize(f, grad, []float64{3, -2}, optimize.MinimizeOptions{
	Lower: []float64{0, math.Inf(-1)},
	Upper: []float64{10, math.Inf(1)},
	Constraints: []optimize.Constraint{{
		Type: optimize.Eq, // Fn(x) = 0; optimize.Ineq means Fn(x) ≥ 0
		Fn:   func(x []float64) float64 { return x[0] + x[1] - 1 },
		Grad: func(x []float64) []float64 { return []float64{1, 1} },
	}},
})
// res.X ≈ (0.5, 0.5), res.F ≈ 0.5, res.Multipliers[0] ≈ 1
```

| Option | Meaning | Default |
|---|---|---|
| `Lower`, `Upper` | per-variable bounds: `nil`, or one entry per variable; an infinite or NaN entry leaves that side open | unbounded |
| `Constraints` | `Constraint{Fn, Grad, Type}`, any mix of `Eq` and `Ineq` | none |
| `Acc` | accuracy goal (SciPy's `ftol`) for the objective change, step and constraint violation | `1e-6` |
| `MaxIter` | major-iteration cap | `100` |

The result carries:

- `X` and `F`: the final point and the objective there.
- `Multipliers`: one Lagrange multiplier per constraint, in your order. At a solution, `∇f = Σ λᵢ ∇Fnᵢ` on the free variables, so `λᵢ` is the rate the optimal objective moves as constraint `i` is relaxed. An `Ineq` multiplier is `≥ 0` when its constraint binds and `0` when it is slack.
- `Active`: per variable, `AtLower`, `AtUpper` or `NotActive`. A variable counts as on a bound when it is within `sqrt(machine epsilon)·(1+|bound|)` of it.
- `MaxViolation`, `Status` (SciPy's exit mode; `Status.String()` is SciPy's message), `Iterations`, `FuncEvals`, `GradEvals`.

A start outside the bounds is clipped to them. A start that violates the general constraints is allowed.

## Errors

The solver never panics.

| Situation | Result | Error |
|---|---|---|
| Bad input: an empty or non-finite start, a nil callback, a bound slice of the wrong length, a lower bound above its upper, a negative tolerance or iteration cap, a constraint with no `Type`, more equalities than variables, or a callback returning a gradient of the wrong length. For `LeastSquares` also: an empty residual, a residual that changes length, a Jacobian of the wrong shape, or a residual or Jacobian that is not finite at the start | `nil` | `PULSE_OPTIMIZE_INVALID` (`details.reason` names the fault) |
| `Minimize` only: the constraints cannot be met (incompatible, singular equalities, or a violation above `10·Acc` at the stop) | the final iterate | `PULSE_OPTIMIZE_INFEASIBLE` |
| Any other early stop, such as the iteration cap (the only short stop for `LeastSquares`) | the final iterate | `PULSE_OPTIMIZE_NOT_CONVERGED` |
| Success | the solution | `nil` |

## Same bits on every architecture

Every product that feeds an addition is an explicit `float64(a*b)` conversion, which stops the compiler from fusing it into a fused multiply-add. Write your callbacks the same way, and route `exp`, `log` and `pow` through deterministic kernels rather than `math`. A problem built that way gives identical bits on arm64 and amd64.
