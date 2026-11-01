```yaml
name: regression-inference
description: Coefficient confidence intervals (ci_lower / ci_upper at conf_level) and the coefficient covariance and correlation on a regression — the per-family critical value, the opt-in vcov flag, where each engine's and modifier's matrix comes from, how it relates to the standard errors, weighted fits, the refusals, and what R's vcov does differently. Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [ci_lower, ci_upper, conf_level, confidence interval, df, vcov, Vcov, Correlation, covariance, coefficient correlation, standard errors, std_errors, regressions]
```

# Regression coefficient intervals and covariance

## Confidence intervals

Every fit with standard errors carries `ci_lower` / `ci_upper` (keyed like `coefficients`), `conf_level` and, for a t interval, `df`: bound = β ∓ q·SE. Set `"conf_level": 0.9` (flat, `0<c<1`, default 0.95).

| Fit | q | `df` |
|---|---|---|
| OLS, ridge | Student t | residual N* − p − 1 (fractional weighted) |
| GLM | normal (Wald, R `confint.default`) | absent |
| Bayesian linear | posterior t, = `credible_intervals` at one level | 2a_N |
| Resample | normal on the resampled SE | absent |

Present exactly where `std_errors` is: lasso / elastic net null on every key (no analytical inference; advisory `PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED`), selection keyed by `selected_features`. An undefined bound (df ≤ 0) is `null`. t on df, so it differs from a pasted ±1.96·SE in about the third digit on small samples.

## Covariance

Set `"vcov": true` on a regression slot (flat spec field, NOT under `params`) to get the coefficient covariance and its correlation. Off by default: `Vcov` / `Correlation` are then absent and the response is byte-identical.

```json
{"regressions": [{"type": "REG_OLS", "target": "mpg", "predictors": ["wt", "hp"], "vcov": true}]}
```

## Shape

`Response.Regressions[i].Vcov` and `.Correlation` are square-symmetric tables in the full encoding, keyed `(intercept)` first, then the predictors in spec order. `Correlation` is cov2cor (unit diagonal); a zero or undefined variance gives `null` in that row and column. Under selection the keys are `(intercept)` + the retained predictors in `selected_features` order; selected-out predictors are absent.

## Where the matrix comes from

| Engine | Matrix |
|---|---|
| OLS | σ̂²(XᵀX)⁻¹, intercept cross terms included (−μᵀCov) |
| Ridge (`l2`) | the sandwich estimator (GLM: H⁻¹XᵀWXH⁻¹, H = XᵀWX + N*·α·D) |
| GLM | φ·(XᵀWX)⁻¹ at the converged IRLS weights: φ = 1 (binomial, poisson, negative binomial at θ̂), gamma's Pearson χ²/df estimate |
| Bayesian linear | the posterior covariance b_N/(a_N−1)·Λ⁻¹; `null` when a_N ≤ 1 |

| Resample `jackknife` | (g−1)/g·Σ(β₋ₖ−β̄)(β₋ₖ−β̄)ᵀ over its g delete-group refits (g = n leave-one-out) |
| Resample `bootstrap` | sample covariance (B−1 divisor) of the successful replicates; seeded = deterministic |
| Selection | the engine's matrix from the final refit on the selected predictors |

For OLS, ridge, GLM and both modifiers, √diag(Vcov) equals `StdErrors` exactly; selection + resample follows the resample SEs over the selected set. The Bayesian diagonal is `StdErrors`² · a_N/(a_N−1), not equal: `StdErrors` there is the scale of the marginal t, not the posterior variance.

Weighted fits (frequency or probability) use the N* basis the weighted SE uses; see [`weighting`](weighting.md).

## Robust standard errors

`"covariance": {"type": "hc3"}` on `REG_OLS` swaps the estimator behind `std_errors`, `p_values`, the intervals and `vcov` (√diag still = `StdErrors`): `hc0`–`hc3`, `cluster` (CR1, OLS `df` = clusters − 1) or `hac` (Newey–West over `order_by`). Absent = `classical`, byte-identical. Matches R `sandwich` + `lmtest::coeftest`.
`REG_GLM` takes it too: the sandwich over the IRLS scores at the converged weights (R `sandwich` on `glm`), dispersion-free, still Wald z with no `df` (cluster and gamma too); weighted, it follows w*.
`REG_BAYES_LINEAR`: Müller's (2013) sandwich posterior N(μ_n, Λ_n⁻¹MΛ_n⁻¹) REPLACES the posterior covariance (mean kept; normal intervals, no `df`) — honest under heteroskedastic or dependent rows, but no longer the conjugate model's probability statement.

## Refusals

`PROCESSING_REGRESSION_VCOV_UNSUPPORTED`, `details.reason` `penalty`: `l1` / `elasticnet` has no sampling covariance of its data-dependent active set, never approximated — refused under resample or selection too. A robust `covariance` forces the buffered path and adds a `Covariance` echo; it is refused (same code) beside any penalty, resample, selection or `bounds`, with < 2 clusters, or a repeated `order_by`. A null, NaN or ±Inf `cluster` / `order_by` value drops the row.

Predict and runtime apply the same rule, so predict reports it before any fit. `pulse_errors_lookup` carries the recovery.

## Against R

- `vcov(lm)` and `vcov(ridge)` agree to rounding.
- `vcov(glm)` uses weights one IRLS iteration behind its coefficients; Pulse inverts at the converged fixed point, matching R refined to that point to 1e-11 (raw `vcov(glm)` only to about 1e-6).
- Negative binomial: θ̂ is MASS `glm.nb`'s ML estimate pushed to its fixed point (glm.nb stops ~1e-6 off it); SEs and `vcov` are `summary.negbin`'s (β, θ orthogonal), `ThetaStdErr` its `SE.theta`, `AIC` counts an estimated θ; a robust `covariance` holds θ̂ fixed, as `sandwich` does (oracle `glm_nb.json`).
- Gamma GLM: dispersion estimated as R does (Pearson χ²/df), so `vcov`, SEs, t p-values and `df` match `summary.glm` / `vcov(glm)` (oracle `glm_gamma.json`); gamma `selection` scores R's `AIC(glm)` / `BIC(glm)` (oracle `glm_gamma_selection.json`).

## Response shaping

`vcov`, `correlation`, `ci_lower`, `ci_upper`, `df`, `conf_level` ride the `standard` preset (the MCP default) and are absent from `minimal`. Exclude with `regressions[*].<key>` paths in `return.exclude`; `return.precision` rounds them like any figure. See [`response-shaping`](response-shaping.md).

## See

- [`regression-modeling`](regression-modeling.md) — choosing a model and modifiers.
- [`op-reg-ols`](op-reg-ols.md) — the `vcov` Param for OLS and ridge.
- [`op-reg-glm`](op-reg-glm.md) — the `vcov` Param for GLM.
- [`op-reg-bayes-linear`](op-reg-bayes-linear.md) — the `vcov` Param for the Bayesian linear model.
- [`weighting`](weighting.md) — the N* basis.
- [`response-shaping`](response-shaping.md) — presets and exclude paths.
- [`matrix-results`](matrix-results.md) — the same matrix encodings.
