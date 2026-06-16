---
name: op-reg-bayes-linear
description: Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior; emits posterior means, std errors, and credible intervals. Streams the same sufficient statistics as REG_OLS.
kind: operator
category: REG
operator: REG_BAYES_LINEAR
type: reference
applies_to: process, compose, predict
examples_tags: [regression, bayesian, streaming-friendly]
---

Regression operators emit coefficient + diagnostics; they do not produce Response.Components. Fit summaries ride `Response.Regressions[i]`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 predictor (numeric). |
| `prior` | enum | `nig` | Only `"nig"` (conjugate Normal-Inverse-Gamma) in v1. |
| `prior_mu` | []float | zero | Prior mean vector; length must match predictor count. |
| `prior_precision` | float | engine | Prior precision scalar Λ₀. |
| `prior_shape` / `prior_rate` | float | engine | Inverse-gamma α₀ / β₀ on residual variance. |
| `credible_level` | float | `0.95` | Posterior credible-interval mass. |

`resample` / `selection` accepted at the spec level but **rejected** at validation — credible intervals already convey uncertainty.

## Inputs

| Param | Accepted field types |
|---|---|
| `target` / `predictors` | numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `packed_bool`, plus nullable variants |

Nullable rows drop from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor βs (posterior means); `StdErrors` from the Student-t marginal posterior; `CredibleIntervals[name] = [lower, upper]` at `credible_level`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs`. **`PValues` not emitted** — Bayesian inference reports credibility, not tail probability. Streams the same Welford stats as `REG_OLS`; one finalize-time Cholesky on `Λ_n` applies the conjugate posterior.

## Gotchas

- `penalty` / `alpha` / `l1_ratio` / `family` / `link` rejected — those knobs belong to other engines.
- `resample` / `selection` rejected — modifiers do not compose here.
- `prior_mu` length must equal predictor count; mismatches → `SERVICE_VALIDATION`.
- Vague-prior limit reproduces the OLS point estimate plus Student-t intervals — sanity check.
- `R2` is computed on the posterior-mean fit, not averaged over posterior draws.

## See

- `pulse_examples_search tags=[bayesian]`
- Skills: `regression-modeling`, `op-reg-ols`, `op-reg-glm`
