---
name: compose-optimize
description: ComposedRequest.optimize — maximise or minimise an expression over bounded decision variables, subject to equality and inequality constraints, using fitted coefficients. How to read the optimum, the marginals (marginal ROI), the multipliers (shadow prices) and the active bounds, the fits-then-optimize pattern, and the failures.
type: guide
kind: design
applies_to: compose, predict
covers: [OptimizeSpec, OptimizeResult, for_each, optimizations, decision variable, objective, constraint, marginal, multiplier, shadow price, active bound, mROI, budget allocation, price optimisation]
requires: [capability:compose_optimize]
---

# Compose optimize

An **optimize block** answers "what values of these knobs give the best outcome?" over a model you fitted in the same batch. It runs once, after the fits (and a bound sweep), before every slot it does not read; it reads no record.

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

- `variables[{name, lower, upper, start?}]`: a bound is a number or a scalar expression read once before the solve: literals, `params.<name>`, `coef()`, `sweep_axis()`, `response()`, `result()`, `lookup()`, built-ins; no variable or field. It must be finite (`bound_not_finite`; else `bound_invalid` / `bound_not_numeric`). `start` defaults to mid-range. A name is an identifier, not `params` / a function.
- Predict range-checks a params-only or loaded-model bound; others are `unresolved_scalars`.
- `objective`: an expression over the variables, `params.<name>`, `coef()` of a batch fit, `response()` (a channel's or geo's steady-state curve, `mmm-workflow`), `hill()`, other functions, `lookup()`. `direction` (`maximize` | `minimize`) is required.
- `constraints[{expr, type}]`: `eq`: `expr = 0`; `ineq`: `expr >= 0`. A budget is `"a + b + c - params.budget"` as `eq`. At most as many `eq` as variables.
- `starts` (default 8, 1..1000): `start` plus seeded Latin-hypercube points; the best converged run wins. `rng_seed` 0 derives it from the spec; the effective seed comes back. `tolerance` (0: 1e-6), `max_iterations` (0: 100). `gradient`: `analytic` (default) or `fd`.<!-- feature: capability:models --> `models` loads saved model files for `coef()` with no refit (`compose-fits`, Loading).<!-- /feature -->
<!-- feature: REG_MMM_BAYES -->
- `posterior`: optimise over Bayes draws (`mmm-bayes-posterior`).
<!-- /feature -->
<!-- feature: capability:for_each -->
- `for_each` writes similar blocks once (`item-fan-out`): `name` must carry an `{{axis}}`; other values vary whole as `{"$var": "axis"}` (a bound, `starts`, a constraint `expr`); `{{axis}}` never inside an expression.
<!-- /feature -->

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

`result("<slot label>", "<aggregation label>")` reads a batch slot's ungrouped aggregation, e.g. `"upper": "result(\"current\", \"spend_tv\") * params.upper_share"`. That slot runs first, once, in its `responses` place. Both arguments are literals (unlabelled slot: `request_<n>`); the slot has no groups / crosstab. Only a block reads it (bounds, starts, objective, constraints), else `PULSE_RESULT_INVALID` `out_of_scope`. Predict refuses an unknown slot / label or a non-number; a null or a failed slot (`details.result_slot`) fails the batch.

## Pattern: fits, then optimize

Fit in `fits`, read `coef("<fit>", "<term>")` in `objective`; the answer is the batch's `optimizations` (outside `return`; `--stream` ends with one `{"optimizations":[...]}` line). Gradients are exact (`hill()`, `coef()`, `response()`, `result()`, `posterior` too); `lookup()`, `let` or an embedder function with no `Derivative` falls back to finite differences (advisory; result `gradient` `fd`, `gradient_fallback` names them).

<!-- feature: capability:compose_sweep_bind -->
With a ranked `sweep`, bind its winner (`fits[].from_sweep`) and read the winning axis values in `objective` with `sweep_axis("<fit>", "<axis>")`, e.g. an adstock steady-state multiplier `(1 - d ^ L) / (1 - d)`: search, fit and optimize in one call (`compose-sweep-bind`).
<!-- /feature -->

## Reading the result

Per block: `variables[{name, value, marginal, active}]`, `constraints[{expr, type, value, multiplier}]`, `converged`, `status`, `iterations`, `max_violation`, `starts`, `starts_agreeing`, `rng_seed`, `gradient`, `gradient_fallback`.

- `value`: the optimum (a flat one to ~sqrt(`tolerance`)).
- `marginal`: d objective / d variable at the optimum (mROI).
- `active`: `lower` or `upper` when the variable sits on that bound.
- `multiplier`: the **shadow price** of a constraint, in the objective's own direction (grad objective = sum of multiplier x grad expr). For a budget `eq`: the gain from one more unit of budget.

Allocation: a free variable's `marginal` equals the budget multiplier; one on its `upper` bound is above it, one at 0 below.

Check `converged`, `starts_agreeing` (few agree: several optima; add `starts`).

## Failures

A block fails alone: no converged start (`PULSE_OPTIMIZE_NOT_CONVERGED`, `_INFEASIBLE` if the constraints cannot hold in the bounds) or a `coef()` of a fit that failed on its data keeps `{name, direction, error}` and warns `PULSE_SLOT_FAILED` with `slot_kind` `optimizations`; `Strict` fails the batch with the block's code. A bad spec is refused up front, predict too: `PULSE_OPTIMIZE_SPEC_INVALID`, `_VARIABLE_INVALID`, `_EXPRESSION_INVALID`, each with `details.optimize` and `details.field`.

## See

`compose-fits` (the fits and `coef()`)<!-- feature: REG_MMM_HILL,capability:compose_sweep_bind --> · `mmm-workflow` (the one-call media-mix allocation)<!-- /feature --> · `compose-requests` · `expression-language` (functions, `params`) · `regression-modeling` · `pulse_examples_search tags=[optimize]`.
