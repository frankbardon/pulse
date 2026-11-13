---
name: op-reg-mmm-bayes
description: REG_MMM_BAYES — Bayesian media-mix regression that adstocks raw spend inside the model and samples the posterior with NUTS, returning ROI, marginal ROI and contribution intervals with sampler diagnostics.
kind: operator
category: REG
operator: REG_MMM_BAYES
type: reference
applies_to: process, compose, predict
examples_tags: [regression, time-series, buffered-pipeline]
---

`REG_MMM_BAYES` models `y = β0 + Σ βc·curve(adstock(rawc; φ); q) + Σ γj·zj + noise`: raw spend is adstocked and saturated in the model with posterior φ and q, so every interval includes them.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | Channels and controls. |
| `channels` | array | (required) | Raw spend (≥ 0); other predictors are controls. |
| `time` | string | (required) | Contiguous integer time, one row per period. |
| `max_lag` | number | 13 | Adstock window; set it unless geometric. |
| `adstock` | string | `geometric` | geometric, delayed, weibull_cdf, weibull_pdf. |
| `saturation` | string | `hill` | hill (`k`, `s`), logistic (`lam`), michaelis_menten (`k`). |
| `priors` | object | — | `priors.<channel>.{roi\|beta}` + the kernel's and curve's keys; `beta` = β mode. |
| `intercept_prior` | object | — | Default normal(0, 2). |
| `control_prior` | object | — | Default normal(0, 1). |
| `sigma_prior` | object | — | Default halfnormal(1). |
| `chains` | number | 4 | 1–16. |
| `warmup` | number | 1000 | 1–20000. |
| `draws` | number | 1000 | 1–20000, per chain. |
| `credible_level` | number | 0.9 | Interval level in (0, 1). |
| `sampler` | string | `nuts` | The only sampler. |
| `target_accept` | number | 0.8 | Raise on divergence. |
| `holdout_from` | number | — | Rows from this time are held out; adstock spans all history. |
| `lift_tests` | array | — | Experiments as data, outcome units. |
| `conf_level` | float | `0.95` | Wald CI on the posterior SD. |
| `vcov` | bool | `false` | Posterior covariance. |
| `name` | string | — | Result name. |

Formulas, priors, lift tests: `mmm-bayes-model`. `rng_seed` 0 derives one from the request.

## Inputs

One market, one row per period. A weekly `date` column is `time_gap`; several rows per period is `duplicate_time`; negative spend is `negative_spend`.

## Output

Coefficients are posterior means<!-- feature: capability:models -->; an `inline` `models` entry declares them with no fit<!-- /feature -->. `outputs`: `seed`, `roi`, `mroi`, `contribution` (per channel), `params`, `diagnostics`, `holdout`; each summary is `{mean, median, sd, lower, upper}` at `credible_level`.

φ and q coefficients: `decay:`/`peak:`/`shape:`/`scale:<ch>`, `k:`/`s:`/`lam:<ch>`. `response()` reads the kernel and curve at their means; `hill()` is Hill only.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- No `save`, scoring, engine `holdout`, `resample` or other shared slot (`rng_seed` excepted): use `params.holdout_from`.
- k and s (or λ) trade off; judge ROI, not k.
- mROI holds the curve fixed.
- Health is reported, not refused: `PROCESSING_REGRESSION_DIAGNOSTIC` with `details.reason` `rhat_high`, `ess_low`, `divergences` or `treedepth_saturated`. `Strict` fails the slot with that code.
- A default fit takes ~35 s.

## See

`mmm-bayes-model` · `mmm-workflow` · `op-reg-mmm-hill` · `regression-model-options` · `regression-spec`
