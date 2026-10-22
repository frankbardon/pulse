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

Regression operators emit coefficient + diagnostics; no `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

## Use when

Fits a straight-line model that predicts a numeric outcome from one or more numeric predictors, by least squares.

Questions it answers:

- Which of price, discount and season are associated with weekly sales, holding the others fixed?
- How much does predicted order value change per extra visit, with the other predictors held fixed?

Use something else:

- `REG_GLM` when the outcome is yes/no (0/1) or a count.

## Params

Every param is a top-level key of the regression entry (manifest `slot: true`); a `params` key is refused `PULSE_REQUEST_UNKNOWN_FIELD`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response variable (numeric). |
| `predictors` | []field | (required) | ≥1 numeric predictor. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`; glmnet `standardize=FALSE` objective (raw units, intercept free). |
| `alpha` | float | — | Strength; >0 when `penalty` set. |
| `l1_ratio` | float | — | Elastic-net mix, `0<r<1` (`elasticnet` only). |
| `unpenalized` | []field | — | `l2` only: listed predictors the ridge term skips (glmnet `penalty.factor` 0). |
| `bounds` | object | — | `{nonnegative: [predictors]}`: those slopes ≥ 0 (NNLS). A binding bound nulls `StdErrors` / `PValues` / CIs / `Vcov` + `PULSE_ADVISORY_CONSTRAINT_ACTIVE`; use `resample: bootstrap`. |
| `max_iters` | int | — | Coordinate-descent iteration cap for regularized fits. |
| `tol` | float | — | Coordinate-descent convergence tolerance for regularized fits. |
| `resample` | `bootstrap` \| `jackknife` | — | Orthogonal resampling layer; downgrades streaming. |
| `bootstrap_iters` | int | `1000` | Replicate count when resample == bootstrap, used as given; <= 0 means 1000. |
| `jackknife_groups` | int | — | resample jackknife only: delete-a-group jackknife over g groups (2 ≤ g ≤ fitted rows) drawn by rng_seed — g refits instead of one per row; SE √((g−1)/g·Σ(β₍ₖ₎−β̄)²), echoed as jackknife_groups. |
| `jackknife_by` | field | — | resample jackknife only: delete-a-cluster jackknife, one replicate per distinct value of this field (≥ 2); a null value drops the row listwise. |
| `rng_seed` | int | — | Seed for the bootstrap RNG and the jackknife_groups assignment; 0 derives a deterministic seed from the request and its rows. |
| `selection` | `backward` \| `forward` \| `stepwise` | — | Subset-selection wrapper; downgrades streaming. |
| `criterion` | `aic` \| `bic` | — | Information criterion driving selection. |
| `conf_level` | float | `0.95` | `CILower` / `CIUpper` coverage, `0<c<1`. |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation`; √diag = `StdErrors`. `l1` / `elasticnet` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. |
| `covariance` | object | — | `{type, cluster, order_by, lags}`: `classical` (= absent), `hc0`–`hc3`, `cluster` (CR1), `hac` (Newey–West; `lags` int or `"auto"`). See [`regression-inference`](regression-inference.md). |
| `weight` | slot weight | — | `null` opts out; both kinds; WLS β, df = N*−p−1, N* = Σw (frequency) / Kish n_eff (probability). |
| `multiplicity` | object | — | `{method, family, alpha, coefficients}`: coefficient `PValues` join a correction family; adds `PAdjusted` / `SignificantAdjusted` beside the raw `PValues` ([`multiplicity-correction`](multiplicity-correction.md)). |
| `save` | string | — | Writes the finished fit as a model file (`.pulse-model.json`, [`compose-fits`](compose-fits.md)). |
| `name` | string | — | Result alias; the saved model's name (default: the `save` file's stem). |

`resample` / `selection` are top-level modifiers — see [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md).

## Inputs

`target` / `predictors` — types: see `## Contract` (nullable too); null rows drop from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs; `StdErrors`, `PValues` (Student-t); `CILower` / `CIUpper`, `DF`, `ConfLevel`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs`; weighted adds `SumWeights` (+ `NEff`, probability). `l2` SEs are the ridge sandwich. `l1` / `elasticnet` without `resample`: SE / p / CI null on EVERY key + `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`. A robust `covariance` adds the `Covariance` echo. Streams sufficient stats; the penalized solve runs once at finalize.

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

- Weighted: `resample: bootstrap` / `selection` → `PULSE_WEIGHT_UNSUPPORTED` (a jackknife is weighted); n_eff ≤ p+1 → `PULSE_WEIGHT_LOW_NEFF`; residual df ≤ 0 makes SEs, p, `CILower`/`CIUpper`, `DF`, `AdjR2`, `ResidualStdErr` null.
- `l1` / `elasticnet` have no analytical inference (the data pick the predictors): SE / p / CI null, advisory not warning, never Strict-escalated. Want uncertainty → [`op-reg-mod-resample`](op-reg-mod-resample.md); want p-values → `l2` or no penalty.
- `unpenalized` (`l2` only; lasso / elastic-net penalty factors are not offered) and `bounds` (no `penalty` / `selection`; robust `covariance` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED` `reason: bounds`): misuse, an unknown or repeated name → `PROCESSING_CONFIG`, predict too. Detail: [`regression-model-options`](regression-model-options.md).
- The penalty reads raw units: divide each predictor by its SD upstream to penalize all alike.
- Robust `covariance` forces the buffered path; beside `penalty` / `resample` / `selection`, < 2 clusters or a repeated `order_by` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`.
- Collinearity → `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`; drop a predictor or add `l2`.
- `penalty != ""` + `selection != ""` → `PROCESSING_REGRESSION_REGULARIZED_SELECTION`.
- Polynomial: stage `FEAT_POLY` polynomial columns upstream; degree gate `[2,10]`; standardize first.
- Per-row residual / fitted / leverage live in `ATTR_REG_*` — separate slot, separate prepass.

## See

- `pulse_examples_search tags=[ols]`
- Skills: [`regression-modeling`](regression-modeling.md), [`regression-model-options`](regression-model-options.md), [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md), [`op-reg-glm`](op-reg-glm.md), [`op-reg-bayes-linear`](op-reg-bayes-linear.md)
