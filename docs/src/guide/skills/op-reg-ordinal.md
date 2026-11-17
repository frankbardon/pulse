```yaml
name: op-reg-ordinal
description: REG_ORDINAL — ordinal (proportional-odds) regression of an ordered response, MASS::polr's model, logit / probit / cloglog; thresholds, Brant and nominal tests, weights, refusals. Always buffered.
kind: operator
category: REG
operator: REG_ORDINAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ordinal]
```

`REG_ORDINAL` fits `P(Y ≤ k | x) = F(ζ_k − xᵀβ)` (polr): one coefficient per predictor shifts every threshold alike.

## Use when

Fits a model that predicts an ordered rating (poor, fair, good) from numeric predictors, each shifting the whole scale up or down.

Questions it answers:

- Which customer traits are associated with a higher satisfaction rating, holding the others fixed?
- How much more likely is a top rating per extra year of tenure?

Use something else:

- `REG_GLM` when the outcome has only two values (yes/no).

## Params

`link` / `levels` / `brant` / `nominal_test` sit under `params`; shared keys: [`regression-spec`](regression-spec.md).

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Categorical or numeric, 3 to 100 levels. |
| `predictors` | []field | (required) | — |
| `link` | enum | `logit` | `logit`, `probit`, `cloglog`. |
| `levels` | array | — | Level order, lowest first. |
| `brant` | object | — | `{}` adds `outputs.brant`; logit only. |
| `nominal_test` | object | — | `{}` adds `outputs.nominal_test`; any link. |
| `conf_level` | float | `0.95` | CI level. |
| `vcov` | bool | `false` | — |
| `covariance` | object | — | `hc0`, `hc1`, `cluster`. |
| `exponentiate` | bool | `false` | — |
| `margins` | object | — | [`regression-margins`](regression-margins.md). |
| `weight` | object | — | — |
| `multiplicity` | object | — | Predictors' p-values only. |
| `holdout` | expr | — | Proper scores. |
| `resample` | enum | — | [`op-reg-mod-resample`](op-reg-mod-resample.md). |
| `bootstrap_iters` | int | `1000` | — |
| `jackknife_groups` | int | — | — |
| `jackknife_by` | field | — | — |
| `rng_seed` | int | — | — |
| `selection` | enum | — | [`op-reg-mod-selection`](op-reg-mod-selection.md). |
| `criterion` | enum | — | `aic`, `bic`. |
| `order` | object | — | Row order. |
| `save` | string | — | Model file ([`regression-row-scores`](regression-row-scores.md)). |
| `name` | string | — | Result name. |

## Inputs

A categorical response reads its labels; a numeric one formats its values (`"3"`, `"2.5"`). A row whose response `levels` omits is dropped, as is a null cluster. Too few / many levels, or a listed level no row carries: `INSUFFICIENT_DATA`, fails alone.

## Output

`coefficients`: predictors (spec order), then thresholds `"<level>|<next>"` (no intercept). `p_values` predictors only, Wald `ci_*`; `deviance`, `null_deviance`, `pseudo_r2`, `two_log_lik` / `aic` (absent under a probability weight). `outputs`: `link`, `levels`, `thresholds`, `fitted_probabilities` (mean P per level), `brant` / `nominal_test` (omnibus, per-predictor `terms`). `exponentiate` adds `exponentiated` (e^β, SE, `ci_*`; predictors); `margins`: dP(level)/dx per `"<level>:<term>"`.

## Reading the output

- `coefficients.*`: A predictor's coefficient is the shift of the latent scale for a one-unit increase in it, holding the other predictors fixed: under the logit link the log odds ratio of a higher rather than a lower level, the same at every step of the scale. A threshold `<level>|<next level>` (listed in outputs.thresholds) is the cut point zeta between two adjacent levels, in P(Y <= level | x) = F(zeta - x'beta).
- `std_errors.*`: How much the coefficient or threshold would vary from sample to sample, from the inverse of the fitted (observed) information matrix.

## Contract

- Types (`target`): `categorical_*`, `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Types (`predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only. Always buffered: the model fits over every admitted row.

## Gotchas

- Dictionary order = import order; else pass `levels`.
- Small Brant / nominal p: an effect varies by step.
- Frequency weights = the `rep()` expansion; probability weights follow `REG_GLM`'s rule (w* = w·n_eff/Σw).
- Refused, predict too (`PROCESSING_CONFIG`, `details.slot`): `penalty`, `collinearity`, `hc2` / `hc3` / `hac`, top-level `link` / `family`.
- `resample` / `selection` refits keep the full fit's levels (`replicate_empty_level`); AIC / BIC k counts thresholds.
- Separation: `NO_CONVERGE`, fails alone.
- `compare`: `lrt` / `wald`, same `levels` ([`regression-comparison`](regression-comparison.md)).

## See

[`regression-modeling`](regression-modeling.md) · [`regression-spec`](regression-spec.md) · [`op-reg-glm`](op-reg-glm.md) · [`regression-inference`](regression-inference.md) · [`weighting`](weighting.md)
