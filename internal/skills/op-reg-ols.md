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

Fits ride `Response.Regressions[i]`; no `Response.Components`.

<!-- generated: use-when -->

## Params

Flat keys (`params` is refused); shared: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | ≥1 numeric. |
| `penalty` | enum | `""` | `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Strength, > 0. |
| `l1_ratio` | float | — | Mix, `0<r<1`. |
| `unpenalized` | []field | — | `l2`: left unshrunk. |
| `bounds` | object | — | `{nonnegative: [...]}`: slopes ≥ 0. |
| `intercept` | bool | `true` | `false`: no intercept. |
| `solver` | enum | `qr` | Or `normal`. |
| `max_iters` | int | — | Penalized cap. |
| `tol` | float | — | Penalized tolerance. |
| `resample` | enum | — | `bootstrap`, `jackknife`. |
| `bootstrap_iters` | int | `1000` | Resample. |
| `jackknife_groups` | int | — | Resample. |
| `jackknife_by` | field | — | Resample. |
| `rng_seed` | int | — | Resample. |
| `selection` | enum | — | `backward`, `forward`, `stepwise`. |
| `criterion` | enum | — | `aic`, `bic`. |
| `conf_level` | float | `0.95` | CI coverage. |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation`. |
| `covariance` | object | — | Robust SEs. |
| `margins` | object | — | Effects = βs (`regression-margins`). |
| `collinearity` | object | — | VIFs (`regression-collinearity`). |
| `weight` | slot weight | — | `null` opts out; WLS. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | p-values join a family; adds `PAdjusted`. |
<!-- /feature -->
<!-- feature: capability:regression_holdout -->
| `holdout` | expr | — | Held-out rows → `Holdout`. |
<!-- /feature -->
<!-- feature: capability:model_save -->
| `save` | string | — | Writes a model file. |
<!-- /feature -->
| `name` | string | — | Result / model name. |

## Inputs

See `## Contract`; null / NaN / ±Inf drops the row.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs; `StdErrors`, `PValues` (Student-t); `CILower` / `CIUpper`, `DF`, `ConfLevel`; `R2`, `AdjR2`, `ResidualStdErr`, `TwoLogLik` / `AIC`, `NObs` (+ weighted `SumWeights`). Robust `covariance` adds a `Covariance` echo.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `l1` / `elasticnet`: null SE / p / CI + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`; use `resample`. The penalty reads raw units (`regression-spec`).
- `unpenalized` (`l2` only) / `bounds` misuse → `PROCESSING_CONFIG`; a binding bound nulls SE / p / CI + `PULSE_ADVISORY_CONSTRAINT_ACTIVE` (`regression-model-options`).
- `vcov` under `l1` / `elasticnet`, or a robust `covariance` (buffered) beside `penalty` / `resample` / `selection` / `bounds` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`.
- Weighted `resample: bootstrap` / `selection` → `PULSE_WEIGHT_UNSUPPORTED`.
- Collinear → `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`: drop one, or `l2`.
- Per-row diagnostics: `ATTR_REG_*`.

## See

- `pulse_examples_search tags=[ols]`
- Skills: `regression-spec`, `regression-modeling`, `regression-margins`, `regression-model-options`, `regression-inference`, `op-reg-mod-resample`, `op-reg-mod-selection`
