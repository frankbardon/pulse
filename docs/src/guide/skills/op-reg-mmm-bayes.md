```yaml
name: op-reg-mmm-bayes
description: REG_MMM_BAYES — Bayesian media-mix regression that adstocks raw spend inside the model and samples the posterior with NUTS (or static HMC), returning ROI, marginal ROI and contribution intervals with sampler diagnostics.
kind: operator
category: REG
operator: REG_MMM_BAYES
type: reference
applies_to: process, compose, predict
examples_tags: [regression, time-series, buffered-pipeline]
```

`REG_MMM_BAYES` models `y = β0 + Σ βc·curve(adstock(rawc; φ); q) + Σ γj·zj + noise`: φ and q are posterior, inside every interval.

## Use when

Fits a media-mix model with intervals: each channel's carry-over, response curve and return on spend, beside linear controls.

Questions it answers:

- What return does each marketing channel earn, and how sure are we?
- How long does each channel's effect carry over?

Use something else:

- `REG_MMM_HILL` when a fast point estimate over already-adstocked channels is enough, or the fit must be saved, scored or optimised.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | Channels and controls. |
| `channels` | array | (required) | Raw spend (≥ 0). |
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
| `sampler` | string | `nuts` | Or `hmc`. |
| `target_accept` | number | 0.8 | Raise if divergent (`geo`: 0.95). |
| `path_length` | number | — | `hmc`: required. |
| `step_size` | number | — | `hmc`: fixed. |
| `holdout_from` | number | — | Time holdout. |
| `geo` | string | — | Panel market field. |
| `per_geo_curves` | bool | `false` | Per-geo curves. |
| `lift_tests` | array | — | Lift experiments. |
| `return_draws` | bool | `false` | `outputs.draws`. |
| `draws_thin` | number | 1 | Every k-th draw. |
| `conf_level` | float | `0.95` | Wald CI. |
| `vcov` | bool | `false` | Posterior vcov. |
| `holdout` | string | — | Engine holdout. |
| `save` | string | — | Model file. |
| `name` | string | — | Result name. |

## Inputs

One row per period (per geo with `geo`; ragged ends ok). Refused: `date` time (`time_gap`; `geo_time_gap`), a repeat (`duplicate_time`), negative spend (`negative_spend`), `[`/`]` in predictors.

## Output

Coefficients are posterior means; an `inline` `models` entry declares them with no fit. `outputs`: `seed`, `roi`, `mroi`, `contribution` (per channel, and `<ch>[<geo>]`), `params`, `diagnostics`, `holdout`, `hierarchy`, `draws`; summaries `{mean, median, sd, lower, upper}` at `credible_level`.

φ and q coefficients: `decay:`/`peak:`/`shape:`/`scale:<ch>`, `k:`/`s:`/`lam:<ch>`. `response(fit, ch, spend[, geo])` reads their means.

## Reading the output

- `std_errors.*`: The posterior standard deviation of each coefficient over every retained draw of every chain: the spread of what the data and the priors allow.
- `p_values.*`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.
- `statistics.*`: Always null: the model reports no test statistic.
- `conf_level`: The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Types (`geo`): `categorical_*`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`); `weight: null` on the slot runs it unweighted.
- Streaming: buffered only. Always buffered: the model fits over every admitted row.

## Gotchas

- `save`, scoring, `holdout` need `return_draws`; scoring reads pre-adstocked channels.
- k and s (or λ) trade off: judge ROI.
- mROI holds the curve fixed.
- Health warns, never refuses (`PROCESSING_REGRESSION_DIAGNOSTIC`, `rhat_high`, `ess_low`, `divergences`, `treedepth_saturated`); `Strict` fails.
- `hmc`: no tree depth; `path_length` π/2 ([`mmm-bayes-posterior`](mmm-bayes-posterior.md)).

## See

[`mmm-bayes-model`](mmm-bayes-model.md) · [`mmm-bayes-posterior`](mmm-bayes-posterior.md) · [`mmm-workflow`](mmm-workflow.md) · [`op-reg-mmm-hill`](op-reg-mmm-hill.md) · [`regression-model-options`](regression-model-options.md) · [`regression-spec`](regression-spec.md)
