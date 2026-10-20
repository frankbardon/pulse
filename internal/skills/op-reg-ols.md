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

Regression operators emit coefficient + diagnostics; no `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

<!-- generated: use-when -->

## Params

Every param is a top-level key of the regression entry (manifest `slot: true`); a `params` key is refused `PULSE_REQUEST_UNKNOWN_FIELD`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 numeric predictor. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`; glmnet `standardize=FALSE` objective (raw units, intercept free). |
| `alpha` | float | `0` | Strength; >0 when `penalty` set. |
| `l1_ratio` | float | `0` | Elastic-net mix, `0<r<1` (`elasticnet` only). |
| `unpenalized` | []field | absent | `l2` only: listed predictors the ridge term skips (glmnet `penalty.factor` 0). |
| `bounds` | object | absent | `{nonnegative: [predictors]}`: those slopes ≥ 0 (NNLS). A binding bound nulls `StdErrors` / `PValues` / CIs / `Vcov` + `PULSE_ADVISORY_CONSTRAINT_ACTIVE`; use `resample: bootstrap`. |
| `max_iters` / `tol` | int / float | engine | Coordinate-descent caps for regularized fits. |
| `weight` | slot weight | inherited | `null` opts out; both kinds; WLS β, df = N*−p−1, N* = Σw (frequency) / Kish n_eff (probability). |
| `conf_level` | float | `0.95` | `CILower` / `CIUpper` coverage, `0<c<1`. |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation`; √diag = `StdErrors`. `l1` / `elasticnet` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. |
| `covariance` | object | absent | `{type, cluster, order_by, lags}`: `classical` (= absent), `hc0`–`hc3`, `cluster` (CR1), `hac` (Newey–West; `lags` int or `"auto"`). See `regression-inference`. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | absent | `{method, family, alpha, coefficients}`: coefficient `PValues` join a correction family; adds `PAdjusted` / `SignificantAdjusted` beside the raw `PValues` (`multiplicity-correction`). |
<!-- /feature -->
| `name` | string | synthesized | Result alias; the saved model's name (default: the `save` file's stem). |
<!-- feature: capability:model_save -->
| `save` | string | absent | Writes the finished fit as a model file (`.pulse-model.json`, `compose-fits`). |
<!-- /feature -->

`resample` / `selection` are top-level modifiers — see `op-reg-mod-resample`, `op-reg-mod-selection`.

## Inputs

`target` / `predictors` — the numeric analytics set (integers, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, nullable too). A categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED`; null rows drop from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs; `StdErrors`, `PValues` (Student-t); `CILower` / `CIUpper`, `DF`, `ConfLevel`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs`; weighted adds `SumWeights` (+ `NEff`, probability). `l2` SEs are the ridge sandwich. `l1` / `elasticnet` without `resample`: SE / p / CI null on EVERY key + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`. A robust `covariance` adds the `Covariance` echo. Streams sufficient stats; the penalized solve runs once at finalize.

<!-- generated: reading-the-output -->

## Gotchas

- Weighted: `resample: bootstrap` / `selection` → `PULSE_WEIGHT_UNSUPPORTED` (a jackknife is weighted); n_eff ≤ p+1 → `PULSE_WEIGHT_LOW_NEFF`; residual df ≤ 0 makes SEs, p, `CILower`/`CIUpper`, `DF`, `AdjR2`, `ResidualStdErr` null.
- `l1` / `elasticnet` have no analytical inference (the data pick the predictors): SE / p / CI null, advisory not warning, never Strict-escalated. Want uncertainty → `op-reg-mod-resample`; want p-values → `l2` or no penalty.
- `unpenalized` (`l2` only; lasso / elastic-net penalty factors are not offered) and `bounds` (no `penalty` / `selection`; robust `covariance` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED` `reason: bounds`): misuse, an unknown or repeated name → `PROCESSING_CONFIG`, predict too. Detail: `regression-model-options`.
- The penalty reads raw units: divide each predictor by its SD upstream to penalize all alike.
- Robust `covariance` forces the buffered path; beside `penalty` / `resample` / `selection`, < 2 clusters or a repeated `order_by` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`.
- Collinearity → `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`; drop a predictor or add `l2`.
- `penalty != ""` + `selection != ""` → `PROCESSING_REGRESSION_REGULARIZED_SELECTION`.
- Polynomial: stage<!-- feature: FEAT_POLY --> `FEAT_POLY`<!-- /feature --> polynomial columns upstream; degree gate `[2,10]`; standardize first.
- Per-row residual / fitted / leverage live in `ATTR_REG_*` — separate slot, separate prepass.

## See

- `pulse_examples_search tags=[ols]`
- Skills: `regression-modeling`, `regression-model-options`, `op-reg-mod-resample`, `op-reg-mod-selection`, `op-reg-glm`, `op-reg-bayes-linear`
