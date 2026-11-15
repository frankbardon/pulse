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

`REG_MMM_HILL` models `y = β0 + Σ βc·hill(ac, kc, sc) + Σ γj·zj`: `ac` is spend adstocked upstream (`WIN_DISTRIBUTED_LAG`), or with `adstock` raw spend the fit adstocks, decay fitted jointly; `zj` linear controls.

## Use when

Fits a media-mix model: each marketing channel's effect and response curve (where its spend stops paying off), jointly with linear controls.

Questions it answers:

- What does each marketing channel contribute to sales, holding price and the other channels fixed?
- At what weekly spend does each channel saturate?

Use something else:

- `REG_OLS` when the outcome moves in a straight line with spend, with no saturation.

## Params

`channels` through `max_lag` sit under `params`; shared keys: [`regression-spec`](regression-spec.md).

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
| `multiplicity` | object | — | Replicate p-values join a family. |
| `holdout` | expr | — | Held-out rows → `Holdout`. |
| `save` | string | — | Writes a model file. |
| `order` | object | — | Row order. |
| `name` | string | — | Result / model name. |

## Inputs

Numeric `target`; channels ≥ 0 (else `PROCESSING_CONFIG`). Null rows drop listwise. Rows > 1 + 3·channels + controls (+ kernel).

## Output

`coefficients` in this order: `(intercept)`, channel betas, controls, `k:<channel>`, `s:<channel>`, then with `adstock` `decay:<channel>` (`delayed`: and `peak:<channel>`). `coef("fit", "k:tv_ad")` reads one; predict knows them from `params`; an `inline` `models` entry declares them with no fit. Bounds: k ∈ [0.02, 20]× channel mean, s ∈ [0.5, 5].

Each channel records its kernel (`adstock.<ch>`, saved too) for `response()`: the fitted one, or its `WIN_DISTRIBUTED_LAG`'s.

`StdErrors` are Gauss-Newton (joint with decay): `null` on a bound and for the k / s / decay of a channel whose β is 0. `PValues`: `resample`. `Vcov`: NaN where held. `outputs.condition_number` (`adstock`) above 1e6 adds `PULSE_ADVISORY_MMM_ADSTOCK_ILL_CONDITIONED`.

## Reading the output

- `coefficients.*`: A channel's coefficient is the most its curve can add to the outcome (the effect at full saturation), in outcome units, holding the other predictors fixed; a control's is the change in the outcome per unit of the control, holding the other predictors fixed; k:&lt;channel&gt; is the spend at which the channel delivers half its maximum and s:&lt;channel&gt; how sharply its curve bends there; under params.adstock, decay:&lt;channel&gt; is the share of a period's spend effect carried into the next (delayed: peak:&lt;channel&gt; is the lag where the carry-over peaks).

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`); `weight: null` on the slot runs it unweighted.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only. Always buffered: the model fits over every admitted row.

## Gotchas

- Spend below k sits on the β/k^s ridge: judge contribution.
- No convergence in 1000 iterations: `PROCESSING_REGRESSION_NO_CONVERGE`, the slot fails alone.
- `penalty`, `selection`, `covariance`, `family`, a weight are refused. `holdout`, `resample`, `save` work, as does `ATTR_REG_PREDICT` (whole model).
- `adstock` needs `order` (one series); refuses `holdout`, an upstream-adstocked channel. Scoring reads pre-adstocked channels.

## See

[`mmm-workflow`](mmm-workflow.md) · [`op-reg-mmm-bayes`](op-reg-mmm-bayes.md) · [`regression-model-options`](regression-model-options.md) · [`regression-spec`](regression-spec.md) · [`compose-sweep-sampling`](compose-sweep-sampling.md) · [`compose-optimize`](compose-optimize.md)
