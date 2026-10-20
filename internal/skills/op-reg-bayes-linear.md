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

Regression operators emit coefficient + diagnostics; no `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 numeric predictor. |
| `prior` | enum | `nig` | Only `"nig"` (conjugate Normal-Inverse-Gamma) in v1. |
| `prior_mu` | []float | zero | Prior mean vector, length predictors + 1, intercept FIRST. |
| `prior_precision` | float or []float | 0.001 | Scalar ε: Λ₀ = ε·I, relative to σ² (prior SD = σ/√ε). Or one entry per coefficient, length predictors + 1, intercept FIRST like `prior_mu`: Λ₀ = diag. `0` = flat on that coefficient; a large entry pins it to its `prior_mu` (an experiment-centred prior). |
| `prior_shape` / `prior_rate` | float | engine | Inverse-gamma α₀ / β₀ on residual variance. |
| `credible_level` | float | `0.95` | Posterior credible-interval mass. |
| `conf_level` | float | `credible_level` | `CILower` / `CIUpper` mass, `0<c<1`. |
| `weight` | slot weight | inherited | `null` opts out; frequency only (= the expanded rows: X'WX, X'Wy, Σw in the posterior). Adds `SumWeights`; `NObs` raw rows. |
| `vcov` | bool | `false` | Adds the POSTERIOR covariance b_n/(a_n−1)·Λ_n⁻¹ (null if a_n ≤ 1) + `Correlation`; diag = `StdErrors`²·a_n/(a_n−1). |
| `covariance` | object | absent | `{type, cluster, order_by, lags}` as `op-reg-ols`. Keeps the posterior-mean βs but REPLACES the posterior covariance with Müller's (2013) sandwich posterior N(μ_n, Λ_n⁻¹MΛ_n⁻¹): `StdErrors`, `CredibleIntervals`, CIs (normal, no `DF`) and `Vcov` (√diag = `StdErrors`) follow it. |

## Inputs

`target` / `predictors` — numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, plus nullable variants. A categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED`. Nullable rows drop from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor posterior-mean βs; `StdErrors` = the Student-t marginal's SCALE (not the posterior SD, which is larger by √(ν/(ν−2))); `CredibleIntervals[name] = [lower, upper]` at `credible_level`; `CILower` / `CIUpper` the same t interval at `conf_level` (equal when unset), `DF` = 2a_n, `ConfLevel`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs`; a robust `covariance` adds the `Covariance` echo. **No `PValues`** — Bayesian inference reports credibility, not tail probability. Streams the same Welford stats as OLS; one finalize-time Cholesky on `Λ_n` applies the conjugate posterior.

<!-- generated: reading-the-output -->

## Gotchas

- A probability weight in force (request, slot or `Options.DefaultWeight`) → `PULSE_WEIGHT_UNSUPPORTED` naming the kind; `"weight": null` runs it unweighted.
- `penalty` / `alpha` / `l1_ratio` / `unpenalized` / `family` / `link` rejected — other engines' knobs.
- `resample` / `selection` → `PROCESSING_CONFIG` (not advertised; predict refuses them too) — credible intervals already convey uncertainty.
- `prior_mu` or a `prior_precision` list of length ≠ predictors + 1, or a negative entry → `PROCESSING_CONFIG` at predict and runtime.
- Flat (`0`) entries need the data to identify those coefficients: an improper posterior (e.g. collinear predictors, both flat) → `PROCESSING_REGRESSION_RANK_DEFICIENT` at fit time.
<!-- feature: capability:multiplicity -->
- A `multiplicity` block resolving to any method but `none` → `PULSE_MULTIPLICITY_INVALID` (no p-values to correct); predict refuses it identically.
<!-- /feature -->
- Robust `covariance` is a substitution, not a posterior: honest under heteroskedastic or dependent rows, but no longer the conjugate model's probability statement. It forces the buffered path; < 2 clusters or a repeated `order_by` → `PROCESSING_REGRESSION_VCOV_UNSUPPORTED`.
- Vague-prior limit reproduces the OLS point estimate; intervals and `ResidualStdErr` run below OLS with few rows per predictor (σ² ≈ RSS/n, not RSS/(n−p−1)).
- `R2` is on the posterior-mean fit, not averaged over posterior draws.

## See

- `pulse_examples_search tags=[bayesian]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-glm`
