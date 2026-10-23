---
name: op-reg-bayes-linear
description: Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior; emits posterior means, std errors, and credible intervals. Streams the same sufficient statistics as an OLS fit.
kind: operator
category: REG
operator: REG_BAYES_LINEAR
type: reference
applies_to: process, compose, predict
examples_tags: [regression, bayesian, streaming-friendly]
---

Fits ride `Response.Regressions[i]`; no `Response.Components`.

<!-- generated: use-when -->

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | ≥1 numeric predictor. |
| `prior` | enum | `nig` | Only `"nig"` (conjugate Normal-Inverse-Gamma). |
| `prior_mu` | []float | — | Prior mean, length predictors + 1, intercept FIRST. |
| `prior_precision` | float or []float | `0.001` | Scalar ε: Λ₀ = ε·I relative to σ² (prior SD = σ/√ε); or one entry per coefficient like `prior_mu` (`0` = flat; `regression-model-options`). |
| `prior_shape` | float | — | Inverse-gamma α₀ on residual variance. |
| `prior_rate` | float | — | Inverse-gamma β₀ on residual variance. |
| `credible_level` | float | `0.95` | Credible-interval mass. |
| `conf_level` | float | `0.95` | `CILower` / `CIUpper` mass. |
| `vcov` | bool | `false` | Adds the posterior covariance + `Correlation` (`regression-inference`). |
| `covariance` | object | — | Müller sandwich posterior (`regression-inference`). |
| `weight` | slot weight | — | `null` opts out; frequency only. |
<!-- feature: capability:model_save -->
| `save` | string | — | Writes a model file. |
<!-- /feature -->
| `name` | string | — | Result / saved-model name. |

## Inputs

`target` / `predictors`: see `## Contract`; a null, NaN or ±Inf drops the row from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor posterior-mean βs; `StdErrors` = the Student-t marginal's SCALE (not the posterior SD, which is larger by √(ν/(ν−2))); `CredibleIntervals[name] = [lower, upper]` at `credible_level`; `CILower` / `CIUpper` the same t interval at `conf_level`, `DF` = 2a_n, `ConfLevel`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs` (+ weighted `SumWeights`). **No `PValues`** — Bayesian inference reports credibility, not tail probability.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Other engines' knobs (`penalty`, `family`, `link`, …) and `resample` / `selection` → `PROCESSING_CONFIG`, predict too (`regression-spec`).
- `prior_mu` or a `prior_precision` list of length ≠ predictors + 1, or a negative entry → `PROCESSING_CONFIG`.
- Flat (`0`) entries need the data to identify those coefficients: an improper posterior (e.g. collinear, both flat) → `PROCESSING_REGRESSION_RANK_DEFICIENT`.
<!-- feature: capability:multiplicity -->
- A `multiplicity` method other than `none` → `PULSE_MULTIPLICITY_INVALID` (no p-values).
<!-- /feature -->
- A robust `covariance` is a substitution, not a posterior (`regression-inference`).
- Vague-prior limit reproduces the OLS point estimate; intervals and `ResidualStdErr` run below OLS with few rows per predictor (σ² ≈ RSS/n, not RSS/(n−p−1)).
- `R2` is on the posterior-mean fit, not averaged over posterior draws.

## See

- `pulse_examples_search tags=[bayesian]`
- Skills: `regression-spec`, `regression-modeling`, `regression-model-options`, `regression-inference`
