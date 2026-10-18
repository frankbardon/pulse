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

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 numeric predictor. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. Minimizes (1/2N*)Σw·r² + `alpha`·[(1−`l1_ratio`)/2‖β‖² + `l1_ratio`‖β‖₁] — glmnet `standardize=FALSE`, the GLM's α too: raw predictor units, intercept unpenalized. |
| `alpha` | float | `0` | Strength; >0 when `penalty` set. |
| `l1_ratio` | float | `0` | Elastic-net mix, `0<r<1` (`elasticnet` only). |
| `max_iters` / `tol` | int / float | engine | Coordinate-descent caps for regularized fits. |
| `weight` | slot weight | inherited | `null` opts out; both kinds. WLS β (penalty × Σw: kind-free); SE, df = N*−p−1 (fractional), `AdjR2` on N* = Σw (frequency) / Kish n_eff (probability). |
| `conf_level` | float | `0.95` | `CILower` / `CIUpper` coverage, `0<c<1`. |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation` (`MatrixValues`, keys `(intercept)` + predictors); √diag = `StdErrors`. Unpenalized σ̂²(XᵀX)⁻¹ / ridge sandwich, N* basis. Resample: replicate covariance; selection: final refit's. `l1` / `elasticnet` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. |
| `covariance` | object | absent | `{type, cluster, order_by, lags}` — the estimator behind `StdErrors`, `PValues`, CIs, `Vcov`. `classical` (= absent); `hc0`–`hc3`; `cluster` (CR1 on `cluster`, categorical / unsigned int, `DF` = G−1); `hac` (Newey–West, `order_by` date / datetime / numeric, `lags` int or `"auto"` = ⌊4(n/100)^(2/9)⌋). Weighted: w* rule. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | absent | `{method, family, alpha, coefficients}`: the member coefficients' `PValues` (default every predictor; `(intercept)` only if listed; unknown name refused) join a correction family — `request` (default, pooled with the tests) or `compose`. Adds `PAdjusted` / `SignificantAdjusted` (vs `alpha`, 0.05) + a `Multiplicity` echo beside the raw `PValues`. Only the regression's own block opts it in (`multiplicity-correction`). |
<!-- /feature -->

`resample` / `selection` are top-level modifiers — see `op-reg-mod-resample`, `op-reg-mod-selection`.

## Inputs

`target` / `predictors` — numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, plus nullable variants. A categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED`. Nullable rows drop from `n_obs` via the per-record bitmap.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs; `StdErrors`, `PValues` (Student-t); `CILower` / `CIUpper` = β ∓ t(`DF`)·SE wherever an SE is (resample: z on its SE, no `DF`), `DF`, `ConfLevel`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs` (raw rows); weighted adds `SumWeights` (+ `NEff`, probability). Penalized shrunk-to-zero βs drop from `StdErrors` and `PValues`. A robust `covariance` adds the `Covariance` echo (`type`, `clusters`, resolved `lags`). Streams Welford-Pébaÿ sufficient stats; the regularized solve runs once at finalize over the p×p Gram.

<!-- generated: reading-the-output -->

## Gotchas

- Weighted: `resample` / `selection` → `PULSE_WEIGHT_UNSUPPORTED`; n_eff ≤ p+1 → `PULSE_WEIGHT_LOW_NEFF`; residual df ≤ 0 makes SEs, p, `CILower`/`CIUpper`, `DF`, `AdjR2`, `ResidualStdErr` null.
- `l1` / `elasticnet` SE is plug-in over the active set — pair with `op-reg-mod-resample`; otherwise `PROCESSING_REGRESSION_APPROXIMATE_SE` warns.
- The penalty reads raw units: a predictor on a large scale is barely shrunk. To penalize all alike, divide each predictor by its SD upstream (a derived column).
- Robust `covariance` forces the buffered path (second pass for residuals / leverage); beside `penalty` / `resample` / `selection`, < 2 clusters or a repeated `order_by` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. A null `cluster` / `order_by` drops the row.
- Collinearity → `PROCESSING_REGRESSION_RANK_DEFICIENT` / `SINGULAR_GRAM`; drop a predictor or add `l2`.
- `penalty != ""` + `selection != ""` → `PROCESSING_REGRESSION_REGULARIZED_SELECTION`.
- Polynomial: stage<!-- feature: FEAT_POLY --> `FEAT_POLY`<!-- /feature --> polynomial columns upstream; degree gate `[2,10]`; standardize first.
- Per-row residual / fitted / leverage live in `ATTR_REG_*` — separate slot, separate prepass.

## See

- `pulse_examples_search tags=[ols]`
- Skills: `regression-modeling`, `op-reg-mod-resample`, `op-reg-mod-selection`, `op-reg-glm`, `op-reg-bayes-linear`
