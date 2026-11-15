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

`REG_MMM_HILL` models `y = β0 + Σ βc·hill(ac, kc, sc) + Σ γj·zj`: `ac` is spend adstocked upstream<!-- feature: WIN_DISTRIBUTED_LAG --> (`WIN_DISTRIBUTED_LAG`)<!-- /feature -->, or with `adstock` raw spend the fit adstocks, decay fitted jointly; `zj` linear controls.

<!-- generated: use-when -->

## Params

`channels` through `max_lag` sit under `params`; shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Response (numeric). |
| `predictors` | []field | (required) | Channels and controls. |
| `channels` | array | (required) | Hill-curve predictors; the rest are controls. |
| `nonnegative` | boolean | `true` | Channel β ≥ 0 (unsupported → exactly 0). |
| `prior` | object | — | Gaussian `{channel, mean, precision}` on one β. |
| `adstock` | string | — | `geometric` / `delayed`: decay fitted in-model. |
| `max_lag` | int | `13` | Adstock window; `delayed` needs it. |
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
| `order` | object | — | Row order. |
| `name` | string | — | Result / model name. |

## Inputs

Numeric `target`; channels ≥ 0 (else `PROCESSING_CONFIG`). Null rows drop listwise. Rows > 1 + 3·channels + controls (+ kernel).

## Output

`coefficients` in this order: `(intercept)`, channel betas, controls, `k:<channel>`, `s:<channel>`, then with `adstock` `decay:<channel>` (`delayed`: and `peak:<channel>`). `coef("fit", "k:tv_ad")` reads one; predict knows them from `params`<!-- feature: capability:models -->; an `inline` `models` entry declares them with no fit<!-- /feature -->. Bounds: k ∈ [0.02, 20]× channel mean, s ∈ [0.5, 5].

Each channel records its kernel (`adstock.<ch>`, saved too) for `response()`: the fitted one, or its<!-- feature: WIN_DISTRIBUTED_LAG --> `WIN_DISTRIBUTED_LAG`'s<!-- /feature -->.

`StdErrors` are Gauss-Newton (joint with decay): `null` on a bound and for the k / s / decay of a channel whose β is 0. `PValues`: `resample`. `Vcov`: NaN where held. `outputs.condition_number` (`adstock`) above 1e6 adds `PULSE_ADVISORY_MMM_ADSTOCK_ILL_CONDITIONED`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Spend below k sits on the β/k^s ridge: judge contribution.
- No convergence in 1000 iterations: `PROCESSING_REGRESSION_NO_CONVERGE`, the slot fails alone.
- `penalty`, `selection`, `covariance`, `family`, a weight are refused. `holdout`, `resample`, `save` work<!-- feature: ATTR_REG_PREDICT -->, as does `ATTR_REG_PREDICT` (whole model)<!-- /feature -->.
- `adstock` needs `order` (one series); refuses `holdout`, an upstream-adstocked channel. Scoring reads pre-adstocked channels.

## See

`mmm-workflow` · `op-reg-mmm-bayes` · `regression-model-options` · `regression-spec` · `compose-sweep-sampling` · `compose-optimize`
