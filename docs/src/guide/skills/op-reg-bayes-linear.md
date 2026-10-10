```yaml
name: op-reg-bayes-linear
description: Bayesian linear regression with a conjugate Normal-Inverse-Gamma prior; emits posterior means, std errors, and credible intervals. Streams the same sufficient statistics as an OLS fit.
kind: operator
category: REG
operator: REG_BAYES_LINEAR
type: reference
applies_to: process, compose, predict
examples_tags: [regression, bayesian, streaming-friendly]
```

Regression operators emit coefficient + diagnostics; no `Response.Components`. Fit summaries ride `Response.Regressions[i]`.

## Use when

Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval.

Questions it answers:

- Given the data and our prior belief, what range of values is plausible for the price coefficient?
- How do the coefficients shift when last year's estimates are used as the prior?

Use something else:

- `REG_GLM` when the outcome is yes/no (0/1) or a count.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | required | Response variable (numeric). |
| `predictors` | []field | required | ≥1 numeric predictor. |
| `prior` | enum | `nig` | Only `"nig"` (conjugate Normal-Inverse-Gamma) in v1. |
| `prior_mu` | []float | zero | Prior mean vector, length predictors + 1, intercept FIRST. |
| `prior_precision` | float | 0.001 | Scalar ε, Λ₀ = ε·I, relative to σ²: prior SD = σ/√ε. |
| `prior_shape` / `prior_rate` | float | engine | Inverse-gamma α₀ / β₀ on residual variance. |
| `credible_level` | float | `0.95` | Posterior credible-interval mass. |
| `weight` | slot weight | inherited | `null` opts out; frequency only (= the expanded rows: X'WX, X'Wy, Σw in the posterior). Adds `SumWeights`; `NObs` raw rows. |
| `vcov` | bool | `false` | Adds the POSTERIOR covariance b_n/(a_n−1)·Λ_n⁻¹ (null if a_n ≤ 1) + `Correlation`; diag = `StdErrors`²·a_n/(a_n−1). |

## Inputs

`target` / `predictors` — numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `packed_bool`, plus nullable variants. Nullable rows drop from `n_obs`.

## Output

`RegressionResult`: `Coefficients["(intercept)"]` + per-predictor posterior-mean βs; `StdErrors` = the Student-t marginal's SCALE (not the posterior SD, which is larger by √(ν/(ν−2))); `CredibleIntervals[name] = [lower, upper]` at `credible_level`; `R2`, `AdjR2`, `ResidualStdErr`, `NObs`. **No `PValues`** — Bayesian inference reports credibility, not tail probability. Streams the same Welford stats as OLS; one finalize-time Cholesky on `Λ_n` applies the conjugate posterior.

## Reading the output

- `coefficients.*`: Posterior mean of the coefficient: the expected difference in the outcome between rows one unit apart on this predictor, holding the other predictors fixed, after combining the data with the prior.
- `std_errors.*`: Scale of the coefficient's Student-t posterior: how uncertain the coefficient is given the data and the prior. With many rows it is close to the posterior standard deviation.
- `r2`: R-squared of the posterior-mean fit: the share of the outcome's variation the posterior-mean coefficients account for in these rows.

## Gotchas

- A probability weight in force (request, slot or `Options.DefaultWeight`) → `PULSE_WEIGHT_UNSUPPORTED` naming the kind; `"weight": null` runs it unweighted.
- `penalty` / `alpha` / `l1_ratio` / `family` / `link` rejected — other engines' knobs.
- `resample` / `selection` → `PROCESSING_CONFIG` (not advertised) — credible intervals already convey uncertainty.
- `prior_mu` length ≠ predictors + 1 → `PROCESSING_CONFIG`.
- Vague-prior limit reproduces the OLS point estimate; intervals and `ResidualStdErr` run below OLS with few rows per predictor (σ² ≈ RSS/n, not RSS/(n−p−1)).
- `R2` is on the posterior-mean fit, not averaged over posterior draws.

## See

- `pulse_examples_search tags=[bayesian]`
- Skills: [`regression-modeling`](regression-modeling.md), [`op-reg-ols`](op-reg-ols.md), [`op-reg-glm`](op-reg-glm.md)
