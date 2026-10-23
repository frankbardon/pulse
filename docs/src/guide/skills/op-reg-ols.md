```yaml
name: op-reg-ols
description: Ordinary least squares with optional l1/l2/elasticnet penalty; covers simple, multiple, ridge, lasso, and elastic-net regression over streaming sufficient statistics.
kind: operator
category: REG
operator: REG_OLS
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, streaming-friendly]
```

Fits ride `Response.Regressions[i]`; no `Response.Components`.

## Use when

Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares.

Questions it answers:

- Which of price, discount and season are associated with weekly sales, holding the others fixed?
- How much does predicted order value change per extra visit, with the other predictors held fixed?

Use something else:

- `REG_GLM` when the outcome is yes/no (0/1) or a count.

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: [`regression-spec`](regression-spec.md).

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
| `covariance` | object | — | Robust SEs ([`regression-inference`](regression-inference.md)). |
| `weight` | slot weight | — | `null` opts out; WLS. |
| `multiplicity` | object | — | Coefficient p-values join a family; adds `PAdjusted`. |
| `holdout` | expr | — | Bool expr; held-out rows → `Holdout`. |
| `save` | string | — | Writes a model file. |
| `name` | string | — | Result / model name. |

## Inputs

`target` / `predictors`: see `## Contract`; null / NaN / ±Inf drops the row from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs; `StdErrors`, `PValues` (Student-t); `CILower` / `CIUpper`, `DF`, `ConfLevel`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs` (+ weighted `SumWeights`). `l1` / `elasticnet` without `resample`: SE / p / CI null + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`. Robust `covariance` adds a `Covariance` echo.

## Reading the output

- `coefficients.*`: The difference in the outcome's expected value between rows one unit apart on this predictor, holding the other predictors fixed, in outcome units per predictor unit (an association, not the effect of changing it).
- `std_errors.*`: How much the coefficient would vary from sample to sample; smaller means a more precise estimate. It is the classic standard error, which assumes residuals of constant spread, unless the request's covariance names a robust one.
- `n_obs`: Rows used in the fit, after rows with a null target or predictor were dropped.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: streamable.

## Gotchas

- `l1` / `elasticnet`: null SE / p / CI is an advisory; use `resample`. The penalty reads raw units ([`regression-spec`](regression-spec.md)).
- `unpenalized` (`l2` only) / `bounds` misuse → `PROCESSING_CONFIG`; a binding bound nulls SE / p / CI + `PULSE_ADVISORY_CONSTRAINT_ACTIVE` ([`regression-model-options`](regression-model-options.md)).
- `vcov` under `l1` / `elasticnet`, or a robust `covariance` (buffered) beside `penalty` / `resample` / `selection` / `bounds` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`.
- Weighted: `resample: bootstrap` / `selection` → `PULSE_WEIGHT_UNSUPPORTED`.
- Collinear → `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`: drop one or `l2`.
- Per-row fitted / residual / leverage: `ATTR_REG_*`.

## See

- `pulse_examples_search tags=[ols]`
- Skills: [`regression-spec`](regression-spec.md), [`regression-modeling`](regression-modeling.md), [`regression-model-options`](regression-model-options.md), [`regression-inference`](regression-inference.md), [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md)
