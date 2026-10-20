---
name: op-reg-glm
description: Generalized linear model via IRLS; supports binomial (logistic), poisson, and gamma families with family-specific link functions. Always buffered.
kind: operator
category: REG
operator: REG_GLM
type: reference
applies_to: process, compose, predict
examples_tags: [regression, glm, logistic, buffered-pipeline]
---

Regression operators emit coefficient + diagnostics; no `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 numeric predictor. |
| `family` | enum | required | `binomial`, `poisson`, `gamma`. |
| `link` | enum | family default | `binomial→logit`, `poisson→log`, `gamma→inverse`. `identity`, `probit`, `cloglog`, `sqrt` reserved. |
| `penalty` | enum | `""` | `l2`, `l1`, `elasticnet` (binomial / poisson). Minimizes −loglik/N* + `alpha`·[(1−`l1_ratio`)/2‖β‖² + `l1_ratio`‖β‖₁] — glmnet `standardize=FALSE`: raw predictor units, intercept unpenalized, N* = Σw. |
| `alpha` / `l1_ratio` | float | — | Strength (>0, glmnet `lambda`) / mix (`elasticnet` only, 0<r<1). |
| `max_iters` / `tol` | int / float | engine | IRLS caps. |
| `weight` | slot weight | inherited | `null` opts out; both kinds. IRLS prior weights w* (R `glm(weights = w*)`); SEs, deviances on w*. |
| `conf_level` | float | `0.95` | `CILower` / `CIUpper` coverage, `0<c<1`. |
| `vcov` | bool | `false` | Adds `Vcov` = (XᵀWX)⁻¹ at the converged weights (dispersion 1; `l2`: sandwich H⁻¹XᵀWXH⁻¹) + `Correlation`; √diag = `StdErrors`. Resample: replicate covariance; selection: final refit's. |
| `covariance` | object | absent | `{type, cluster, order_by, lags}` as `op-reg-ols`: `hc0`–`hc3`, `cluster` (CR1), `hac` (Newey–West). Sandwich over the IRLS scores at the converged weights (R `sandwich` on `glm`) behind `StdErrors`, `PValues`, CIs, `Vcov`; still Wald z (no `DF`, cluster too). Weighted: w* rule. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | absent | `{method, family, alpha, coefficients}` as `op-reg-ols`: the member coefficients' Wald p-values join a `request` / `compose` family; adds `PAdjusted` / `SignificantAdjusted` + echo beside `PValues`. |
<!-- /feature -->

`resample` / `selection` are top-level modifiers — see `op-reg-mod-resample`, `op-reg-mod-selection`.

## Inputs

`target` / `predictors` — numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, plus nullable variants. A categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED`. Binomial targets accept `{0,1}` or `packed_bool`. Nullable rows drop from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs (link scale); `StdErrors`, `PValues` (Wald-z from `Cov(β) = (XᵀWX)⁻¹`); `CILower` / `CIUpper` = Wald β ∓ z·SE (R `confint.default`, not the profile `confint`), `ConfLevel`, no `DF`; `Deviance`, `NullDeviance`, `PseudoR2` = 1 − D/D₀ (McFadden only for 0/1 binomial); `Family`, `Link` (+ `Penalty`, `Alpha`, `L1Ratio`) echoed; `l2` SEs are the H = XᵀWX + N*·α·D sandwich; `l1` / `elasticnet` without `resample`: `StdErrors` / `PValues` / CIs null on every key + `Advisories` `[PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED]`; `ConvergedIters` = IRLS steps (R's per-row start, step-halving); `NObs` raw rows, weighted adds `SumWeights` (+ `NEff`, probability); a robust `covariance` adds the `Covariance` echo. Always buffered — IRLS needs multiple passes.

<!-- generated: reading-the-output -->

## Gotchas

- Weighted: `resample: bootstrap` / `selection` → `PULSE_WEIGHT_UNSUPPORTED` (a jackknife is weighted); n_eff < p+1 → `PULSE_WEIGHT_LOW_NEFF`.
- `penalty` on gamma, or `alpha` / `l1_ratio` without one → `PROCESSING_CONFIG`. `l1` / `elasticnet`: no SE / p / CI (null; advisory) unless `resample`, `vcov` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`; + `selection` warns `PROCESSING_REGRESSION_REGULARIZED_SELECTION`.
- Binomial separation (a predictor splits the outcome, e.g. an empty 2×2 cell) → `PROCESSING_REGRESSION_SEPARATION`, `details.predictors` named, no coefficients (the MLE is infinite); the slot fails alone (`error`). Add `penalty: l2` (finite fit), or drop / merge it; check it is not recorded after the outcome. A strong but unseparated predictor converges.
- Other non-convergence → `PROCESSING_REGRESSION_NO_CONVERGE` (`details.reason`): raise `max_iters`, rescale.
- Unsupported link → `PROCESSING_REGRESSION_INVALID_LINK`.
- Dispersion fixed at 1 for binomial / poisson, and gamma (weighted too) inherits it — be sceptical of gamma SEs and overdispersed counts; `covariance` `hc0`–`hc3` is dispersion-free.
- Robust `covariance` beside `resample` / `selection`, < 2 clusters or a repeated `order_by` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. A null `cluster` / `order_by` drops the row.
- Collinear or constant predictor → `PROCESSING_REGRESSION_RANK_DEFICIENT` from the up-front rank check, `details.columns` naming it; drop it.

## See

- `pulse_examples_search tags=[glm]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-bayes-linear`, `op-reg-mod-resample`, `op-reg-mod-selection`
