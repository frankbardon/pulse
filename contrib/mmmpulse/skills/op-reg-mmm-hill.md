---
name: op-reg-mmm-hill
description: REG_MMM_HILL (contrib/mmmpulse) — media-mix regression fitting every channel's coefficient and Hill curve (half-saturation k, shape s) jointly, plus linear controls. Params, coefficient names, standard-error rule, failures.
kind: operator
category: REG
operator: REG_MMM_HILL
type: reference
applies_to: process, compose, predict
---

`REG_MMM_HILL` models `y = β0 + Σ βc·hill(ac, kc, sc) + Σ γj·zj`: `ac` are already-adstocked spend columns (adstocked upstream<!-- feature: WIN_DISTRIBUTED_LAG --> by `WIN_DISTRIBUTED_LAG`<!-- /feature -->, decay a sweep axis), `zj` linear controls. It does not adstock. Registered by `mmmpulse.Extensions()`; unregistered it is `PULSE_REGRESSION_INVALID`.

<!-- generated: use-when -->

## Params

Under `params`; target and predictors as on any regression.

| Name | Type | Default | Description |
|---|---|---|---|
| `channels` | array | (required) | Predictors entering through the Hill curve; every other predictor is a linear control. |
| `nonnegative` | boolean | `true` | Each channel β ≥ 0, so an unsupported channel is exactly 0. |
| `prior` | object | — | Gaussian prior `{channel, mean, precision}` on one channel's β (an experiment-measured effect). An unknown key is `PROCESSING_CONFIG`. |

## Inputs

Numeric `target`; channels ≥ 0 (a negative value is `PROCESSING_CONFIG`). Null rows drop listwise. Needs more rows than 1 + 3·channels + controls.

## Output

`coefficients` in this order: `(intercept)`, channel betas, controls, `k:<channel>` for each channel, `s:<channel>` for each channel. Read a curve with `coef("fit", "k:tv_ad")` (`s:` alike; `stat` `se` works). `outputs.k` / `outputs.s` repeat them keyed by channel. Bounds: k within [0.02, 20]× the channel mean, s within [0.5, 5].

`StdErrors` are Gauss-Newton, per parameter: `null` only for a parameter on a bound and for the k / s of a channel whose β is 0. This differs from the plain OLS bound fit<!-- feature: REG_OLS --> (`REG_OLS`)<!-- /feature -->, which nulls every SE when any bound binds: here the free block still has a valid covariance, so a binding channel does not blind the rest. `PValues` come from `resample` (bootstrap, a Wald statistic per coefficient); `Vcov` is NaN in a held parameter's row and column.
<!-- generated: contract -->

## Gotchas

- An underfunded channel (spend never reaches its half-saturation) sits on the β/k^s ridge: its k and s are weakly identified even when its ROI and lift are right. Judge it by contribution, not by k.
- No convergence in 1000 iterations is `PROCESSING_REGRESSION_NO_CONVERGE`; the slot fails alone (`PULSE_SLOT_FAILED`), a sweep goes on.
- `penalty`, `selection`, `covariance`, `family` are `PROCESSING_CONFIG`. `holdout`, `resample` and `save` work<!-- feature: ATTR_REG_PREDICT --> as does scoring with `ATTR_REG_PREDICT`<!-- /feature -->.
- A decay interval is a spread, not one SE: bootstrap the fit, and read the decays of the sweep's near-best slots (`ext-mmm-workflow`).

## See

`ext-mmm-workflow` · `regression-model-options` · `compose-sweep-sampling` · `compose-optimize`
