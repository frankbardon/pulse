---
name: op-reg-mmm-hill
description: REG_MMM_HILL — media-mix regression fitting every channel's coefficient and Hill curve (half-saturation k, shape s) jointly, plus linear controls. Params, coefficient names, standard-error rule, failures. Always buffered.
kind: operator
category: REG
operator: REG_MMM_HILL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, time-series, buffered-pipeline]
---

`REG_MMM_HILL` models `y = β0 + Σ βc·hill(ac, kc, sc) + Σ γj·zj`: `ac` are already-adstocked spend columns (adstocked upstream<!-- feature: WIN_DISTRIBUTED_LAG --> by `WIN_DISTRIBUTED_LAG`<!-- /feature -->, decay a sweep axis), `zj` linear controls. It does not adstock. Fits ride `Response.Regressions[i]`.

<!-- generated: use-when -->

## Params

Flat keys, but `channels` / `nonnegative` / `prior` sit under `params`; shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | Channels and controls. |
| `channels` | array | (required) | Predictors entering the Hill curve; the rest are linear controls. |
| `nonnegative` | boolean | `true` | Channel β ≥ 0: an unsupported channel is exactly 0. |
| `prior` | object | — | Gaussian prior `{channel, mean, precision}` on one channel's β. |
| `resample` | enum | — | `bootstrap`, `jackknife`. |
| `bootstrap_iters` | int | `1000` | Resample. |
| `jackknife_groups` | int | — | Resample. |
| `jackknife_by` | field | — | Resample. |
| `rng_seed` | int | — | Resample. |
| `conf_level` | float | `0.95` | CI coverage. |
| `vcov` | bool | `false` | Adds `Vcov` + `Correlation`. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | Replicate p-values join a family. |
<!-- /feature -->
<!-- feature: capability:regression_holdout -->
| `holdout` | expr | — | Held-out rows → `Holdout`. |
<!-- /feature -->
<!-- feature: capability:model_save -->
| `save` | string | — | Writes a model file. |
<!-- /feature -->
| `name` | string | — | Result / model name. |

## Inputs

Numeric `target`; channels ≥ 0 (a negative value is `PROCESSING_CONFIG`). Null rows drop listwise. Needs more rows than 1 + 3·channels + controls.

## Output

`coefficients` in this order: `(intercept)`, channel betas, controls, `k:<channel>` for each channel, `s:<channel>` for each channel. Read one with `coef("fit", "k:tv_ad")` (`se` works); predict knows these names from `params.channels`. `outputs.k` / `outputs.s` repeat them keyed by channel. Bounds: k within [0.02, 20]× the channel mean, s within [0.5, 5].

`StdErrors` are Gauss-Newton, per parameter: `null` only for a parameter on a bound and for the k / s of a channel whose β is 0. Unlike a bounded OLS fit<!-- feature: REG_OLS --> (`REG_OLS`)<!-- /feature -->, a binding channel does not null the rest. `PValues` come from `resample` (bootstrap, a Wald statistic per coefficient); `Vcov` is NaN in a held parameter's row and column.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- An underfunded channel (spend never reaches k) sits on the β/k^s ridge: judge it by contribution, not by k.
- No convergence in 1000 iterations is `PROCESSING_REGRESSION_NO_CONVERGE`; the slot fails alone (`PULSE_SLOT_FAILED`), a sweep goes on.
- `penalty`, `selection`, `covariance`, `family`, a weight are refused. `holdout`, `resample` and `save` work<!-- feature: ATTR_REG_PREDICT --> as does scoring with `ATTR_REG_PREDICT` (whole model: no `terms`, no bounds)<!-- /feature -->.
- A decay interval is a spread, not one SE: bootstrap the fit, and read the decays of the sweep's near-best slots.

## See

`regression-model-options` · `regression-spec` · `compose-sweep-sampling` · `compose-optimize`
