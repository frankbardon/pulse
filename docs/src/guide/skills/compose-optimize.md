```yaml
name: compose-optimize
description: ComposedRequest.optimize — maximise or minimise an expression over bounded decision variables, subject to equality and inequality constraints, using fitted coefficients. How to read the optimum, the marginals (marginal ROI), the multipliers (shadow prices) and the active bounds, the fits-then-optimize pattern, and the failures.
type: guide
kind: design
applies_to: compose, predict
covers: [OptimizeSpec, OptimizeResult, optimizations, decision variable, objective, constraint, marginal, multiplier, shadow price, active bound, mROI, budget allocation, price optimisation]
requires: [capability:compose_optimize]
```

# Compose optimize

An **optimize block** answers "what values of these knobs give the best outcome?" over a model you fitted in the same batch. It runs once, before any slot, reads no record, and is independent of the slots.

```jsonc
{
  "params": {"cost": 5},
  "fits": [{"name": "demand", "request": {"...": "one regression: units on price"}}],
  "requests": [{"...": "any slot"}],
  "optimize": [{
    "name": "price", "direction": "maximize",
    "variables": [{"name": "p", "lower": 1, "upper": 30}],
    "objective": "(p - params.cost) * (coef(\"demand\", \"(intercept)\") + coef(\"demand\", \"price\") * p)"
  }]
}
```

## The block

- `variables[{name, lower, upper, start?}]`: a bound is a number or `"params.<name>"`. `start` defaults to the midpoint. A name is an identifier, not `params` or a function.
- `objective`: an expression over the variables, `params.<name>`, `coef()` of a batch fit, `hill()`, other expression functions, `lookup()`. `direction` (`maximize` | `minimize`) is required.
- `constraints[{expr, type}]`: `eq` means `expr = 0`, `ineq` means `expr >= 0`. A budget is `"a + b + c - params.budget"` as `eq`. At most as many `eq` as variables.
- `starts` (default 8, 1..1000): the solver runs from `start` plus `starts` minus one seeded Latin-hypercube points inside the bounds and reports the best converged one. `rng_seed` 0 derives the seed from the spec alone; the effective seed comes back. `tolerance` (0 = 1e-6) and `max_iterations` (0 = 100). `models` loads saved model files for `coef()` with no refit ([`compose-fits`](compose-fits.md), Loading).

## Pattern: fits, then optimize

Fit in `fits`, read the coefficients in `objective` with `coef("<fit>", "<term>")`, and the answer is on the same batch's `optimizations` (a response slot outside `return`; `--stream` ends with one `{"optimizations":[...]}` line). Gradients are finite differences, so any smooth expression works; a coefficient that is a regression output needs no hand-copy.

## Reading the result

Per block: `variables[{name, value, marginal, active}]`, `constraints[{expr, type, value, multiplier}]`, `converged`, `status`, `iterations`, `max_violation`, `starts`, `starts_agreeing`, `rng_seed`.

- `value`: the optimal setting. At the default tolerance a flat optimum is only accurate to about the square root of `tolerance` in the value; tighten `tolerance` when the setting itself matters.
- `marginal`: d objective / d variable at the optimum. In an allocation this is the marginal ROI of one more unit of that variable.
- `active`: `lower` or `upper` when the variable sits on that bound.
- `multiplier`: the **shadow price** of a constraint, in the objective's own direction (gradient of objective = sum of multiplier x gradient of expr). For a budget `eq`, it is the gain from one more unit of budget.

Allocation reading: every free variable (not on a bound) has `marginal` equal to the budget multiplier, so marginal ROI is equalised. A variable at its `upper` bound has `marginal` above the multiplier (raise the cap to gain); one at 0 has `marginal` below it (it is not worth a unit).

Trust it by `converged` and `starts_agreeing`: if few starts agree with the best, the surface has several optima or a flat ridge; raise `starts` or inspect the bounds.

## Failures

A block fails alone: no converged start (`PULSE_OPTIMIZE_NOT_CONVERGED`, `PULSE_OPTIMIZE_INFEASIBLE` when the constraints cannot hold inside the bounds) or a `coef()` of a fit that failed on its data keeps `{name, direction, error}` and warns `PULSE_SLOT_FAILED` with `slot_kind` `optimizations`; `Strict` fails the batch with the block's code. A bad spec is refused before anything runs, by predict and the runtime alike: `PULSE_OPTIMIZE_SPEC_INVALID`, `_VARIABLE_INVALID`, `_EXPRESSION_INVALID`, each with `details.optimize` and `details.field`.

## See

`compose-fits` (the fits and `coef()`) · `compose-requests` · `expression-language` (functions, `params`) · `regression-modeling` · `pulse_examples_search tags=[compose, ols]`.
