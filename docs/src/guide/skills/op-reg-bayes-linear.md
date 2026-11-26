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

Fits ride `regressions[i]`; no `Response.Components`.

## Use when

Fits a straight-line model of a numeric outcome under a prior, reporting each coefficient's posterior mean and credible interval.

Questions it answers:

- Given the data and our prior belief, what range of values is plausible for the price coefficient?
- How do the coefficients shift when last year's estimates are used as the prior?

Use something else:

- `REG_GLM` when the outcome is yes/no (0/1) or a count.

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: [`regression-spec`](regression-spec.md).

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | ≥1 numeric predictor. |
| `prior` | enum | `nig` | Only `"nig"` (conjugate Normal-Inverse-Gamma). |
| `prior_mu` | []float | — | Prior mean, length predictors + 1, intercept FIRST. |
| `prior_precision` | float or []float | `0.001` | Scalar ε: Λ₀ = ε·I relative to σ² (prior SD = σ/√ε); or one entry per coefficient like `prior_mu` (`0` = flat; [`regression-model-options`](regression-model-options.md)). |
| `prior_shape` | float | — | Inverse-gamma α₀ on residual variance. |
| `prior_rate` | float | — | Inverse-gamma β₀ on residual variance. |
| `credible_level` | float | `0.95` | Credible-interval mass. |
| `conf_level` | float | `0.95` | `ci_lower` / `ci_upper` mass. |
| `vcov` | bool | `false` | Adds the posterior covariance + `correlation`. |
| `covariance` | object | — | Müller sandwich posterior ([`regression-inference`](regression-inference.md)). |
| `weight` | slot weight | — | `null` opts out; frequency only. |
| `holdout` | expr | — | Held-out rows → `holdout`. |
| `save` | string | — | Writes a model file. |
| `order` | object | — | Row order. |
| `name` | string | — | Result / model name. |

## Inputs

`target` / `predictors`: see `## Contract`; a null, NaN or ±Inf drops the row from `n_obs`.

## Output

`coefficients["(intercept)"]` + per-predictor posterior-mean βs; `std_errors` = the Student-t marginal's SCALE (not the posterior SD, which is larger by √(ν/(ν−2))); `credible_intervals[name] = [lower, upper]` at `credible_level`; `ci_lower` / `ci_upper` the same t interval at `conf_level`, `df` = 2a_n, `conf_level`; `r2`, `adj_r2`, `residual_std_err`, `n_obs` (+ weighted `sum_weights`). **No `p_values`** — credibility, not tail probability.

## Reading the output

- `coefficients.*`: Posterior mean of the coefficient: the expected difference in the outcome between rows one unit apart on this predictor, holding the other predictors fixed, after combining the data with the prior.
- `std_errors.*`: Scale of the coefficient's Student-t posterior: how uncertain the coefficient is given the data and the prior. With many rows it is close to the posterior standard deviation.
- `conf_level`: The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` weights only; a `probability` weight is `PULSE_WEIGHT_UNSUPPORTED`.
- Streaming: streamable.

## Gotchas

- Other engines' knobs and `resample` / `selection` → `PROCESSING_CONFIG`, predict too ([`regression-spec`](regression-spec.md)).
- `prior_mu` or a `prior_precision` list of length ≠ predictors + 1, or a negative entry → `PROCESSING_CONFIG`.
- Flat (`0`) entries need the data to identify those coefficients: an improper posterior (e.g. collinear, both flat) → `PROCESSING_REGRESSION_RANK_DEFICIENT`.
- A `multiplicity` method other than `none` → `PULSE_MULTIPLICITY_INVALID` (no p-values).
- A robust `covariance` is a substitution, not a posterior ([`regression-inference`](regression-inference.md)).
- Vague-prior limit reproduces the OLS point estimate; intervals and `residual_std_err` run below OLS with few rows per predictor (σ² ≈ RSS/n, not RSS/(n−p−1)).
- `r2` is on the posterior-mean fit, not averaged over posterior draws.

## See

- `pulse_examples_search tags=[bayesian]`
- Skills: [`regression-spec`](regression-spec.md), [`regression-modeling`](regression-modeling.md), [`regression-model-options`](regression-model-options.md), [`regression-inference`](regression-inference.md)
