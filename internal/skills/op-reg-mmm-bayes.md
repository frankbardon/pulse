---
name: op-reg-mmm-bayes
description: REG_MMM_BAYES — Bayesian media-mix regression that adstocks raw spend inside the model and samples the posterior with NUTS (or static HMC), returning ROI, marginal ROI and contribution intervals with sampler diagnostics.
kind: operator
category: REG
operator: REG_MMM_BAYES
type: reference
applies_to: process, compose, predict
examples_tags: [regression, time-series, buffered-pipeline]
---

`REG_MMM_BAYES` models `y = β0 + Σ βc·curve(adstock(rawc; φ); q) + Σ γj·zj + noise`: φ and q are posterior, inside every interval.

<!-- generated: use-when -->

## Params

`channels` through `carryover` sit under `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | Channels and controls. |
| `channels` | list | (required) | Raw spend (≥ 0). |
| `time` | string | — | Deprecated: `order.field`. |
| `max_lag` | float | 13 | Adstock window. |
| `adstock` | string | `geometric` | geometric, delayed, weibull_cdf, weibull_pdf. |
| `saturation` | string | `hill` | hill (`k`, `s`), logistic (`lam`), michaelis_menten (`k`). |
| `priors` | object | — | Per channel `{roi\|beta}` + kernel/curve keys. |
| `intercept_prior` | object | — | normal(0, 2). |
| `control_prior` | object | — | normal(0, 1). |
| `sigma_prior` | object | — | halfnormal(1). |
| `chains` | float | 4 | 1–16. |
| `warmup` | float | 1000 | 1–20000. |
| `draws` | float | 1000 | Per chain. |
| `credible_level` | float | 0.9 | — |
| `sampler` | string | `nuts` | Or `hmc`. |
| `target_accept` | float | 0.8 | Raise on divergences (`geo` 0.95). |
| `path_length` | float | — | `hmc`: required. |
| `step_size` | float | — | `hmc`: fixed. |
| `holdout_from` | float | — | Time holdout. |
| `geo` | string | — | Panel market field. |
| `per_geo_curves` | bool | `false` | — |
| `lift_tests` | list | — | — |
| `return_draws` | bool | `false` | `outputs.draws`. |
| `draws_thin` | float | 1 | Every k-th draw. |
| `carryover` | bool | `true` | Holdout carries adstock. |
| `conf_level` | float | `0.95` | Wald CI. |
| `vcov` | bool | `false` | Posterior vcov. |
| `holdout` | string | — | Row holdout. |
| `save` | string | — | — |
| `order` | object | — | Time field (+ `geo` partition). |
| `name` | string | — | — |

## Inputs

One row per period (per geo with `geo`; ragged ends ok). Refused: a gap (`time_gap`, `geo_time_gap`; weekly `date`s), a repeat (`duplicate_time`), negative spend, `[` `]` in names.

## Output

Coefficients are posterior means<!-- feature: capability:models -->; an `inline` `models` entry declares them with no fit<!-- /feature -->. `outputs`: `seed`, `roi`, `mroi`, `contribution` (per channel, and `<ch>[<geo>]`), `params`, `diagnostics`, `holdout`, `hierarchy`, `draws`; summaries `{mean, median, sd, lower, upper}` at `credible_level`.

φ and q coefficients: `decay:`/`peak:`/`shape:`/`scale:<ch>`, `k:`/`s:`/`lam:<ch>`. `response()` reads their means.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `save`, scoring, `holdout` need `return_draws`; the last two carry adstock (`regression-order`).
- k and s (or λ) trade off: judge ROI.
- Health warns, never refuses (`PROCESSING_REGRESSION_DIAGNOSTIC`, `rhat_high`, `ess_low`, `divergences`, `treedepth_saturated`); `Options.Strict` fails the slot.

## See

`mmm-bayes-model` · `mmm-bayes-posterior` · `mmm-workflow` · `op-reg-mmm-hill` · `regression-model-options` · `regression-spec`
