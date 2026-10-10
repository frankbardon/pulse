```yaml
name: regression-inference
description: Coefficient covariance and correlation on a regression — the opt-in vcov flag, where each engine's matrix comes from, how it relates to the standard errors, weighted fits, the refusals, and what R's vcov does differently. Topical design; per-model params in atomic op-reg-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [vcov, Vcov, Correlation, covariance, coefficient correlation, standard errors, std_errors, regressions]
```

# Regression coefficient covariance

Set `"vcov": true` on a regression slot (flat spec field, NOT under `params`) to get the coefficient covariance and its correlation. Off by default: `Vcov` / `Correlation` are then absent and the response is byte-identical.

```json
{"regressions": [{"type": "REG_OLS", "target": "mpg", "predictors": ["wt", "hp"], "vcov": true}]}
```

## Shape

`Response.Regressions[i].Vcov` and `.Correlation` are square-symmetric tables in the full encoding, keyed `(intercept)` first, then the predictors in spec order. `Correlation` is cov2cor (unit diagonal); a zero or undefined variance gives `null` in that row and column. Selected-out predictors do not exist here: selection is refused.

## Where the matrix comes from

| Engine | Matrix |
|---|---|
| OLS | σ̂²(XᵀX)⁻¹, intercept cross terms included (−μᵀCov) |
| Ridge (`l2`) | the sandwich estimator |
| GLM | (XᵀWX)⁻¹ at the converged IRLS weights, dispersion fixed at 1 |
| Bayesian linear | the posterior covariance b_N/(a_N−1)·Λ⁻¹; `null` when a_N ≤ 1 |

For OLS, ridge and GLM, √diag(Vcov) equals `StdErrors` exactly. The Bayesian diagonal is `StdErrors`² · a_N/(a_N−1), not equal: `StdErrors` there is the scale of the marginal t, not the posterior variance.

Weighted fits (frequency or probability) use the N* basis the weighted SE uses; see [`weighting`](weighting.md).

## Refusals

`PROCESSING_REGRESSION_VCOV_UNSUPPORTED`, `details.reason` one of:

| reason | Cause |
|---|---|
| `penalty` | `l1` / `elasticnet`: no closed-form covariance |
| `resample` | jackknife / bootstrap rewrite the SE, so the matrix would disagree with it |
| `selection` | the active set is data-dependent |

Predict and runtime apply the same rule, so predict reports it before any fit. `pulse_errors_lookup` carries the recovery.

## Against R

- `vcov(lm)` and `vcov(ridge)` agree to rounding.
- `vcov(glm)` uses weights one IRLS iteration behind its coefficients, so Pulse (the converged fixed point) matches it to about 1e-6, not exactly.
- Gamma GLM: Pulse fixes dispersion at 1, R estimates it, so the covariances differ by that factor. No oracle case covers gamma.

## Response shaping

Both slots ride the `standard` preset (the MCP default) and are absent from `minimal`. Exclude with `regressions[*].vcov` / `regressions[*].correlation` paths in `return.exclude`; `return.precision` rounds them like any figure. See [`response-shaping`](response-shaping.md).

## See

- [`regression-modeling`](regression-modeling.md) — choosing a model and modifiers.
- [`op-reg-ols`](op-reg-ols.md), [`op-reg-glm`](op-reg-glm.md), [`op-reg-bayes-linear`](op-reg-bayes-linear.md) — the `vcov` Param per engine.
- [`weighting`](weighting.md) — the N* basis.
- [`response-shaping`](response-shaping.md) — presets and exclude paths.
- [`matrix-results`](matrix-results.md) — the same matrix encodings.
