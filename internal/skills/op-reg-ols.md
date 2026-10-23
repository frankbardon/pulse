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

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | ≥1 numeric predictor. |
| `penalty` | enum | `""` | `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Strength, > 0. |
| `l1_ratio` | float | — | Elastic-net mix, `0<r<1`. |
| `unpenalized` | []field | — | `l2`: predictors left unshrunk. |
| `bounds` | object | — | `{nonnegative: [predictors]}`: slopes ≥ 0 (NNLS). |
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
| `covariance` | object | — | Robust SEs (`regression-inference`). |
| `weight` | slot weight | — | `null` opts out; WLS. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | Coefficient p-values join a family; adds `PAdjusted`. |
<!-- /feature -->
<!-- feature: capability:regression_holdout -->
| `holdout` | expr | — | Held-out rows → `Holdout`. |
<!-- /feature -->
<!-- feature: capability:model_save -->
| `save` | string | — | Writes a model file. |
<!-- /feature -->
| `name` | string | — | Result / model name. |

## Inputs

`target` / `predictors`: see `## Contract`; null / NaN / ±Inf drops the row from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs; `StdErrors`, `PValues` (Student-t); `CILower` / `CIUpper`, `DF`, `ConfLevel`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs` (+ weighted `SumWeights`). `l1` / `elasticnet` without `resample`: SE / p / CI null + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`. Robust `covariance` adds a `Covariance` echo.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `l1` / `elasticnet`: the null SE / p / CI is an advisory; uncertainty → `resample`. The penalty reads raw units (`regression-spec`).
- `unpenalized` (`l2` only) / `bounds` misuse → `PROCESSING_CONFIG`; a binding bound nulls SE / p / CI + `PULSE_ADVISORY_CONSTRAINT_ACTIVE` (`regression-model-options`).
- `vcov` under `l1` / `elasticnet`, or a robust `covariance` (buffered) beside `penalty` / `resample` / `selection` / `bounds` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`.
- Weighted: `resample: bootstrap` / `selection` → `PULSE_WEIGHT_UNSUPPORTED`.
- Collinear → `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`: drop one or add `l2`.
- Per-row fitted / residual / leverage: `ATTR_REG_*`.

## See

- `pulse_examples_search tags=[ols]`
- Skills: `regression-spec`, `regression-modeling`, `regression-model-options`, `regression-inference`, `op-reg-mod-resample`, `op-reg-mod-selection`
