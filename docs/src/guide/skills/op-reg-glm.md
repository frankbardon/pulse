```yaml
name: op-reg-glm
description: Generalized linear model via IRLS; supports binomial (logistic), poisson, and gamma families with family-specific link functions. Always buffered.
kind: operator
category: REG
operator: REG_GLM
type: reference
applies_to: process, compose, predict
examples_tags: [regression, glm, logistic, buffered-pipeline]
```

Regression operators emit coefficient + diagnostics; no `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

## Use when

Fits a generalized linear model: logistic regression for yes/no outcomes, Poisson for counts, gamma for positive skewed amounts.

Questions it answers:

- Which customer traits are associated with the chance of churning, holding the others fixed?
- How does the expected number of support tickets change with account size?

Use something else:

- `TEST_PROP_Z` when you only want to know whether a yes/no rate differs between two groups.

## Params

Every param is a top-level key of the regression entry (manifest `slot: true`); a `params` key is refused `PULSE_REQUEST_UNKNOWN_FIELD`.

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
| `vcov` | bool | `false` | Adds `Vcov` = φ·(XᵀWX)⁻¹ at the converged weights (φ = 1; gamma φ̂; `l2`: sandwich H⁻¹XᵀWXH⁻¹) + `Correlation`; √diag = `StdErrors`. Resample: replicate covariance; selection: final refit's. |
| `covariance` | object | absent | `{type, cluster, order_by, lags}` as [`op-reg-ols`](op-reg-ols.md): `hc0`–`hc3`, `cluster` (CR1), `hac` (Newey–West). Sandwich over the IRLS scores at the converged weights (R `sandwich` on `glm`) behind `StdErrors`, `PValues`, CIs, `Vcov`; dispersion-free, still Wald z (no `DF`; cluster, gamma too). Weighted: w* rule. |
| `multiplicity` | object | absent | `{method, family, alpha, coefficients}` as [`op-reg-ols`](op-reg-ols.md): the member coefficients' p-values join a `request` / `compose` family; adds `PAdjusted` / `SignificantAdjusted` + echo beside `PValues`. |

`resample` / `selection` are top-level modifiers — see [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md).

## Inputs

`target` / `predictors` — numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, plus nullable variants. A categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED`. Binomial targets accept `{0,1}` or `packed_bool`. Nullable rows drop from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs (link scale); `StdErrors`, `PValues` from `Cov(β) = φ·(XᵀWX)⁻¹`: binomial / poisson φ = 1, Wald z, `CILower` / `CIUpper` = β ∓ z·SE (R `confint.default`, not the profile `confint`), no `DF`; gamma φ̂ = Pearson χ²/`DF`, `DF` = N* − p − 1 (N* = n, Σw, or n_eff), t p-values and β ∓ t·SE (R `summary.glm`; φ̂ not emitted; `DF` ≤ 0 → SE / p / CI / `DF` null); `ConfLevel`; `Deviance`, `NullDeviance`, `PseudoR2` = 1 − D/D₀ (McFadden only for 0/1 binomial); `Family`, `Link` (+ `Penalty`, `Alpha`, `L1Ratio`) echoed; `l2` SEs are the H = XᵀWX + N*·α·D sandwich; `l1` / `elasticnet` without `resample`: `StdErrors` / `PValues` / CIs null on every key + `Advisories` `[PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED]`; `ConvergedIters` = IRLS steps (R's per-row start, step-halving); `NObs` raw rows, weighted adds `SumWeights` (+ `NEff`, probability); a robust `covariance` adds the `Covariance` echo. Always buffered — IRLS needs multiple passes.

## Reading the output

- `coefficients.*`: The change in the linear predictor, on the link scale, for a one-unit increase in this predictor, holding the other predictors fixed: log-odds for binomial (logit link), the log of the expected count for Poisson (log link), the inverse of the expected value for gamma (inverse link).
- `std_errors.*`: How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix: with the dispersion fixed at 1 for binomial and Poisson, and scaled by the estimated dispersion for gamma.

## Gotchas

- Weighted: `resample: bootstrap` / `selection` → `PULSE_WEIGHT_UNSUPPORTED` (a jackknife is weighted); n_eff < p+1 (gamma: ≤) → `PULSE_WEIGHT_LOW_NEFF`.
- `penalty` on gamma, or `alpha` / `l1_ratio` without one → `PROCESSING_CONFIG`. `l1` / `elasticnet`: no SE / p / CI (null; advisory) unless `resample`, `vcov` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`; + `selection` warns `PROCESSING_REGRESSION_REGULARIZED_SELECTION`.
- Binomial separation (a predictor splits the outcome, e.g. an empty 2×2 cell) → `PROCESSING_REGRESSION_SEPARATION`, `details.predictors` named, no coefficients (the MLE is infinite); the slot fails alone (`error`). Add `penalty: l2` (finite fit), or drop / merge it; check it is not recorded after the outcome. A strong but unseparated predictor converges.
- Other non-convergence → `PROCESSING_REGRESSION_NO_CONVERGE` (`details.reason`): raise `max_iters`, rescale.
- Unsupported link → `PROCESSING_REGRESSION_INVALID_LINK`.
- Dispersion fixed at 1 for binomial / poisson — overdispersed counts get too-small SEs; gamma estimates it (weighted too). `covariance` `hc0`–`hc3` is dispersion-free. Gamma `selection` scores R's `AIC(glm)` / `BIC(glm)` (φ = D/n counted), not D + 2k.
- Robust `covariance` beside `resample` / `selection`, < 2 clusters or a repeated `order_by` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`. A null `cluster` / `order_by` drops the row.
- Collinear or constant predictor → `PROCESSING_REGRESSION_RANK_DEFICIENT` from the up-front rank check, `details.columns` naming it; drop it.

## See

- `pulse_examples_search tags=[glm]`
- Skills: [`regression-modeling`](regression-modeling.md), [`op-reg-ols`](op-reg-ols.md), [`op-reg-bayes-linear`](op-reg-bayes-linear.md), [`op-reg-mod-resample`](op-reg-mod-resample.md), [`op-reg-mod-selection`](op-reg-mod-selection.md)
