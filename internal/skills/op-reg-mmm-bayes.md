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

`REG_MMM_BAYES` models `y = β0 + Σ βc·hill(adstock(rawc; decay), k, s) + Σ γj·zj + noise`: raw spend is geometrically adstocked in the model, so decay is a posterior parameter and every interval includes it. Fits ride `Response.Regressions[i]`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | Channels and controls. |
| `channels` | array | (required) | Raw spend predictors (≥ 0); other predictors are linear controls. |
| `time` | string | (required) | Integer time field, one row per period, contiguous with step 1. |
| `max_lag` | number | 13 | Adstock window in periods. |
| `adstock` | string | `geometric` | The only kernel. |
| `priors` | object | — | `priors.<channel>.{roi\|beta, decay, k, s}`; a `beta` prior switches that channel to β mode. |
| `intercept_prior` | object | — | Default normal(0, 2). |
| `control_prior` | object | — | Default normal(0, 1). |
| `sigma_prior` | object | — | Default halfnormal(1). |
| `chains` | number | 4 | 1–16. |
| `warmup` | number | 1000 | 1–20000. |
| `draws` | number | 1000 | 1–20000, per chain. |
| `credible_level` | number | 0.9 | Interval level in (0, 1). |
| `sampler` | string | `nuts` | The only sampler. |
| `target_accept` | number | 0.8 | Raise it when the fit diverges. |
| `holdout_from` | number | — | Time from which rows are held out; adstock still runs over the full history. |
| `conf_level` | float | `0.95` | Wald CI on the posterior SD. |
| `vcov` | bool | `false` | Posterior covariance. |
| `name` | string | — | Result name. |

Prior object: `{family, mu, sigma, alpha, beta, lower, upper}` (normal, halfnormal, lognormal, beta, truncnormal), each family its own keys. Defaults: decay Beta(1,1), s LogNormal(0.3, 0.5), k TruncNormal(1, 1, 0.02, 10), ROI LogNormal(0.2, 0.9). `rng_seed` 0 derives a seed from the request, key order ignored.

## Inputs

One market, one row per period. A weekly `date` column is `time_gap`; several rows per period is `duplicate_time`; negative spend is `negative_spend`.

## Output

Coefficients are posterior means. `outputs`: `seed`, `roi`, `mroi`, `contribution` (per channel), `params`, `diagnostics`, `holdout`; each summary is `{mean, median, sd, lower, upper}` at `credible_level`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- No `save`, scoring, engine `holdout`, `resample` or other shared slot (`rng_seed` excepted): use `params.holdout_from`.
- k and s trade off against each other; judge ROI and contribution, not k.
- mROI is analytic with the curve held fixed.
- Health is reported, not refused: `PROCESSING_REGRESSION_DIAGNOSTIC` with `details.reason` `rhat_high`, `ess_low`, `divergences` or `treedepth_saturated`. `Strict` fails the slot with that code. 2×100 draws always warn `ess_low`.
- A default fit takes about 35 s; set `rng_seed` to reproduce it.

## See

`mmm-workflow` · `op-reg-mmm-hill` · `regression-model-options` · `regression-spec`
