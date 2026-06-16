---
name: op-reg-ols
description: Ordinary least squares with optional l1/l2/elasticnet penalty; covers simple, multiple, ridge, lasso, and elastic-net regression over streaming sufficient statistics.
kind: operator
category: REG
operator: REG_OLS
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, streaming-friendly]
---

Regression operators emit coefficient + diagnostics; they do not produce Response.Components. Fit summaries ride `Response.Regressions[i]`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 predictor (numeric). |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | `0` | Strength; >0 when `penalty` set. |
| `l1_ratio` | float | `0` | Elastic-net mix in `[0,1]`. |
| `max_iters` / `tol` | int / float | engine | Coordinate-descent caps for regularized fits. |

`resample` / `selection` are top-level modifiers — see `op-reg-mod-resample`, `op-reg-mod-selection`.

## Inputs

| Param | Accepted field types |
|---|---|
| `target` / `predictors` | numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `packed_bool`, plus nullable variants |

Nullable rows drop from `n_obs` via the per-record bitmap.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs; `StdErrors`, `PValues` (Student-t); `R2`, `AdjR2`, `ResidualStdErr`, `NObs`. Penalized shrunk-to-zero βs drop from `StdErrors`. Streams Welford-Pébaÿ sufficient stats; regularized solve runs once at finalize over the p×p Gram.

## Gotchas

- `l1` / `elasticnet` SE is plug-in over the active set — pair with `op-reg-mod-resample`; otherwise `PROCESSING_REGRESSION_APPROXIMATE_SE` warns.
- Collinearity → `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`; drop a predictor or add `l2`.
- `penalty != ""` + `selection != ""` → `PROCESSING_REGRESSION_REGULARIZED_SELECTION`.
- Polynomial: stage `FEAT_POLY` upstream; degree gate `[2,10]`; standardize first.
- Per-row residual / fitted / leverage live in `ATTR_REG_*` — separate slot, separate prepass.

## See

- `pulse_examples_search tags=[ols]`
- Skills: `regression-modeling`, `op-reg-mod-resample`, `op-reg-mod-selection`, `op-reg-glm`, `op-reg-bayes-linear`
