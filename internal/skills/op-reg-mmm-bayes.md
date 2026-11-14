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

`REG_MMM_BAYES` models `y = β0 + Σ βc·curve(adstock(rawc; φ); q) + Σ γj·zj + noise`: φ and q are posterior, so every interval includes them.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | Channels and controls. |
| `channels` | array | (required) | Raw spend (≥ 0); others are controls. |
| `time` | string | (required) | Contiguous integers. |
| `max_lag` | number | 13 | Adstock window. |
| `adstock` | string | `geometric` | geometric, delayed, weibull_cdf, weibull_pdf. |
| `saturation` | string | `hill` | hill (`k`, `s`), logistic (`lam`), michaelis_menten (`k`). |
| `priors` | object | — | Per channel `{roi\|beta}` + kernel/curve keys. |
| `intercept_prior` | object | — | normal(0, 2). |
| `control_prior` | object | — | normal(0, 1). |
| `sigma_prior` | object | — | halfnormal(1). |
| `chains` | number | 4 | 1–16. |
| `warmup` | number | 1000 | 1–20000. |
| `draws` | number | 1000 | Per chain. |
| `credible_level` | number | 0.9 | Interval level. |
| `sampler` | string | `nuts` | — |
| `target_accept` | number | 0.8 | Raise if divergent (`geo`: 0.95). |
| `holdout_from` | number | — | History-aware holdout. |
| `geo` | string | — | Market field (panel). |
| `per_geo_curves` | bool | `false` | Per-geo pooled curves. |
| `lift_tests` | array | — | Experiments as data. |
| `return_draws` | bool | `false` | Adds `outputs.draws`. |
| `draws_thin` | number | 1 | Keep every k-th draw. |
| `conf_level` | float | `0.95` | Wald CI. |
| `vcov` | bool | `false` | Posterior covariance. |
| `holdout` | string | — | Engine holdout. |
| `save` | string | — | Model file. |
| `name` | string | — | Result name. |

Model detail: `mmm-bayes-model`.

## Inputs

One row per period (per geo with `geo`; ragged ends ok). A `date` time is `time_gap` (`geo_time_gap` in a geo); a repeat `duplicate_time`; negative spend `negative_spend`. No `[`/`]` in predictor names.

## Output

Coefficients are posterior means<!-- feature: capability:models -->; an `inline` `models` entry declares them with no fit<!-- /feature -->. `outputs`: `seed`, `roi`, `mroi`, `contribution` (per channel, and `<ch>[<geo>]`), `params`, `diagnostics`, `holdout`, `hierarchy`, `draws`; each summary is `{mean, median, sd, lower, upper}` at `credible_level`.

φ and q coefficients: `decay:`/`peak:`/`shape:`/`scale:<ch>`, `k:`/`s:`/`lam:<ch>`. `response(fit, ch, spend[, geo])` reads them at their means.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `save`, scoring, `holdout` need `return_draws`; scoring reads pre-adstocked channels (`mmm-bayes-posterior`).
- k and s (or λ) trade off: judge ROI.
- mROI holds the curve fixed.
- Health warns, never refuses: `PROCESSING_REGRESSION_DIAGNOSTIC`, `details.reason` `rhat_high`, `ess_low`, `divergences` or `treedepth_saturated`; `Strict` fails the slot.

## See

`mmm-bayes-model` · `mmm-bayes-posterior` · `mmm-workflow` · `op-reg-mmm-hill` · `regression-model-options` · `regression-spec`
