---
name: compose-optimize
description: ComposedRequest.optimize — maximise or minimise an expression over bounded decision variables, subject to equality and inequality constraints, using fitted coefficients. How to read the optimum, the marginals (marginal ROI), the multipliers (shadow prices) and the active bounds, the fits-then-optimize pattern, and the failures.
type: guide
kind: design
applies_to: compose, predict
covers: [OptimizeSpec, OptimizeResult, optimizations, decision variable, objective, constraint, marginal, multiplier, shadow price, active bound, mROI, budget allocation, price optimisation]
requires: [capability:compose_optimize]
---

# Compose optimize

An **optimize block** answers "what values of these knobs give the best outcome?" over a model you fitted in the same batch. It runs once, after the fits (and the sweep when a fit binds its winner) and before every slot it does not read; it reads no record.

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

- `variables[{name, lower, upper, start?}]`: a bound is a number or a scalar expression read once before the solve: literals, `params.<name>`, `coef()`, `sweep_axis()`, `response()`, `result()`, `lookup()`, built-ins; no variable or field. It must be finite (`bound_not_finite`; else `bound_invalid` / `bound_not_numeric`). `start` defaults to the midpoint. A name is an identifier, not `params` or a function.
- Predict range-checks a params-only bound; one calling a bound function is listed under `unresolved_scalars` {`path`, `expression`, `reads`} (not a warning) and range-checked at run time.
- `objective`: an expression over the variables, `params.<name>`, `coef()` of a batch fit, `response()` (a media-mix channel's steady-state curve, `mmm-workflow`), `hill()`, other expression functions, `lookup()`. `direction` (`maximize` | `minimize`) is required.
- `constraints[{expr, type}]`: `eq` means `expr = 0`, `ineq` means `expr >= 0`. A budget is `"a + b + c - params.budget"` as `eq`. At most as many `eq` as variables.
- `starts` (default 8, 1..1000): the solver runs from `start` plus `starts` minus one seeded Latin-hypercube points inside the bounds and reports the best converged one. `rng_seed` 0 derives the seed from the spec alone; the effective seed comes back. `tolerance` (0 = 1e-6) and `max_iterations` (0 = 100).<!-- feature: capability:models --> `models` loads saved model files for `coef()` with no refit (`compose-fits`, Loading).<!-- /feature -->

<!-- feature: capability:models,REG_OLS -->
## An inline truth model

A `models` entry may declare the model instead of naming a file — exactly one of `path` / `inline`. The body is the model-file document; any number in it may be an expression over literals and `params.<name>` (no `coef()` or other bound function). One objective then serves a fit in one block and a known truth in another:

```jsonc
"models": [{"name": "demand", "inline": {"model_format_version": 1, "type": "REG_OLS", "target": "units",
  "predictors": ["price"], "terms": ["price"], "coefficients": {"(intercept)": "params.a", "price": "params.b"}}}]
```

`coef()`, `sweep_axis()` (from `sweep_axes`) and `response()` (from `adstock`) read it; scoring it is `inline_not_scorable`. Coefficients name exactly the terms the type reports (`PULSE_MODEL_REF_INVALID` `inline_term_unknown` / `_missing`). Predict resolves every number.
<!-- /feature -->

## Bounds from data: result()

`result("<slot label>", "<aggregation label>")` reads an ungrouped aggregation of a batch slot, e.g. `"upper": "result(\"current\", \"spend_tv\") * params.upper_share"`. That slot runs first, once, and keeps its place in `responses`. Both arguments are literals (an unlabelled slot is `request_<n>`); the slot has no groups or crosstab. Only a block reads it (bounds, starts, objective, constraints); elsewhere it is `PULSE_RESULT_INVALID` `out_of_scope`. Predict refuses an unknown slot or label; a null value fails the batch at run time.

## Pattern: fits, then optimize

Fit in `fits`, read the coefficients in `objective` with `coef("<fit>", "<term>")`, and the answer is on the same batch's `optimizations` (a response slot outside `return`; `--stream` ends with one `{"optimizations":[...]}` line). Gradients are finite differences: any smooth expression works.

<!-- feature: capability:compose_sweep_bind -->
With a ranked `sweep`, bind its winner (`fits[].from_sweep`) and read the winning axis values in `objective` with `sweep_axis("<fit>", "<axis>")`, e.g. an adstock steady-state multiplier `(1 - d ^ L) / (1 - d)`: search, fit and optimize in one call (`compose-sweep-bind`).
<!-- /feature -->

## Reading the result

Per block: `variables[{name, value, marginal, active}]`, `constraints[{expr, type, value, multiplier}]`, `converged`, `status`, `iterations`, `max_violation`, `starts`, `starts_agreeing`, `rng_seed`.

- `value`: the optimal setting; a flat optimum is accurate to about the square root of `tolerance`.
- `marginal`: d objective / d variable at the optimum. In an allocation this is the marginal ROI of one more unit of that variable.
- `active`: `lower` or `upper` when the variable sits on that bound.
- `multiplier`: the **shadow price** of a constraint, in the objective's own direction (gradient of objective = sum of multiplier x gradient of expr). For a budget `eq`, it is the gain from one more unit of budget.

Allocation: a free variable's `marginal` equals the budget multiplier (marginal ROI equalised); one on its `upper` bound sits above it (raise the cap), one at 0 below.

Trust it by `converged` and `starts_agreeing` (few agreeing: several optima or a flat ridge; raise `starts`).

## Failures

A block fails alone: no converged start (`PULSE_OPTIMIZE_NOT_CONVERGED`, `PULSE_OPTIMIZE_INFEASIBLE` when the constraints cannot hold inside the bounds) or a `coef()` of a fit that failed on its data keeps `{name, direction, error}` and warns `PULSE_SLOT_FAILED` with `slot_kind` `optimizations`; `Strict` fails the batch with the block's code. A bad spec is refused before anything runs, by predict and the runtime alike: `PULSE_OPTIMIZE_SPEC_INVALID`, `_VARIABLE_INVALID`, `_EXPRESSION_INVALID`, each with `details.optimize` and `details.field`.

## See

`compose-fits` (the fits and `coef()`)<!-- feature: REG_MMM_HILL,capability:compose_sweep_bind --> · `mmm-workflow` (the one-call media-mix allocation)<!-- /feature --> · `compose-requests` · `expression-language` (functions, `params`) · `regression-modeling` · `pulse_examples_search tags=[compose, ols]`.
