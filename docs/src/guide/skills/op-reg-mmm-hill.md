```yaml
name: op-reg-mmm-hill
description: REG_MMM_HILL — media-mix regression fitting every channel's coefficient and Hill curve (half-saturation k, shape s) jointly, plus linear controls. Params, coefficient names, standard-error rule, failures. Always buffered.
kind: operator
category: REG
operator: REG_MMM_HILL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, time-series, buffered-pipeline]
```

`REG_MMM_HILL` models `y = β0 + Σ βc·hill(ac, kc, sc) + Σ γj·zj`: `ac` are already-adstocked spend columns (adstocked upstream by `WIN_DISTRIBUTED_LAG`, decay a sweep axis), `zj` linear controls. It does not adstock.

## Use when

Fits a media-mix model: each marketing channel's effect and response curve (where its spend stops paying off), jointly with linear controls.

Questions it answers:

- What does each marketing channel contribute to sales, holding price and the other channels fixed?
- At what weekly spend does each channel saturate?

Use something else:

- `REG_OLS` when the outcome moves in a straight line with spend, with no saturation.

## Params

Flat keys, but `channels` / `nonnegative` / `prior` sit under `params`; shared keys: [`regression-spec`](regression-spec.md).

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
| `multiplicity` | object | — | Replicate p-values join a family. |
| `holdout` | expr | — | Held-out rows → `Holdout`. |
| `save` | string | — | Writes a model file. |
| `name` | string | — | Result / model name. |

## Inputs

Numeric `target`; channels ≥ 0 (a negative value is `PROCESSING_CONFIG`). Null rows drop listwise. Needs more rows than 1 + 3·channels + controls.

## Output

`coefficients` in this order: `(intercept)`, channel betas, controls, `k:<channel>` for each channel, `s:<channel>` for each channel. Read one with `coef("fit", "k:tv_ad")` (`se` works); predict knows these names from `params.channels`. `outputs.k` / `outputs.s` repeat them keyed by channel. Bounds: k within [0.02, 20]× the channel mean, s within [0.5, 5].

`StdErrors` are Gauss-Newton, per parameter: `null` only for a parameter on a bound and for the k / s of a channel whose β is 0. Unlike a bounded OLS fit (`REG_OLS`), a binding channel does not null the rest. `PValues` come from `resample` (bootstrap, a Wald statistic per coefficient); `Vcov` is NaN in a held parameter's row and column.

## Reading the output

- `coefficients.*`: A channel's coefficient is the most its curve can add to the outcome (the effect at full saturation), in outcome units, holding the other predictors fixed; a control's is the change in the outcome per unit of the control, holding the other predictors fixed; k:&lt;channel&gt; is the spend at which the channel delivers half its maximum and s:&lt;channel&gt; how sharply its curve bends there.
- `std_errors.*`: How much the parameter would vary from sample to sample, from the curvature of the least-squares fit at its optimum (Gauss-Newton), mapped onto k and s.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`); `weight: null` on the slot runs it unweighted.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only. Always buffered: the model fits over every admitted row.

## Gotchas

- An underfunded channel (spend never reaches k) sits on the β/k^s ridge: judge it by contribution, not by k.
- No convergence in 1000 iterations is `PROCESSING_REGRESSION_NO_CONVERGE`; the slot fails alone (`PULSE_SLOT_FAILED`), a sweep goes on.
- `penalty`, `selection`, `covariance`, `family`, a weight are refused. `holdout`, `resample` and `save` work as does scoring with `ATTR_REG_PREDICT` (whole model: no `terms`, no bounds).
- A decay interval is a spread, not one SE: bootstrap the fit, and read the decays of the sweep's near-best slots.

## See

[`mmm-workflow`](mmm-workflow.md) · [`op-reg-mmm-bayes`](op-reg-mmm-bayes.md) · [`regression-model-options`](regression-model-options.md) · [`regression-spec`](regression-spec.md) · [`compose-sweep-sampling`](compose-sweep-sampling.md) · [`compose-optimize`](compose-optimize.md)
