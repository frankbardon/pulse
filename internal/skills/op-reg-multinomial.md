---
name: op-reg-multinomial
description: Multinomial (baseline-category) logistic regression of a categorical target on numeric predictors; one coefficient set per non-reference level, log odds against the reference. Always buffered.
kind: operator
category: REG
operator: REG_MULTINOMIAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, multinomial, buffered-pipeline]
---

Fits ride `Response.Regressions[i]`; no `Response.Components`.

<!-- generated: use-when -->

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: `regression-spec`.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | A categorical cohort field; its dictionary order is the category order. |
| `predictors` | []field | (required) | ≥1 numeric predictor. |
| `reference` | string | — | Baseline category, one of the target's labels; absent: the first fitted level. |
| `max_iters` | int | `50` | Newton-Raphson iteration cap. |
| `tol` | float | `0.00000001` | Relative step tolerance (quadratic convergence: the MLE to double precision). |
| `conf_level` | float | `0.95` | Wald `CILower` / `CIUpper` mass. |
| `vcov` | bool | `false` | Inverse information over the keys + `Correlation`. |
| `weight` | slot weight | — | `null` opts out; frequency or probability. |
<!-- feature: capability:multiplicity -->
| `multiplicity` | object | — | Default family: every non-intercept key; `coefficients` lists `<level>:<term>` keys. |
<!-- /feature -->
| `name` | string | — | Result name. |

## Inputs

`target`: categorical (`categorical_u8/u16/u32`); a numeric target is `PULSE_FIELD_TYPE_UNSUPPORTED`, a derived column `PROCESSING_CONFIG` (`requires: categorical_target`). `predictors`: see `## Contract`; a null, NaN or ±Inf drops the row from `n_obs`.

## Output

`RegressionResult`: `Reference`, `Categories` (fitted levels, dictionary order); `Coefficients` / `StdErrors` / `PValues` (Wald z) / `CILower` / `CIUpper` keyed `"<level>:<term>"` per non-reference level, the intercept `"<level>:(intercept)"` (nnet's vcov names); `Deviance` (−2ℓ), `NullDeviance`, `PseudoR2` (McFadden), `TwoLogLik`, `AIC` (+ 2·K·(p+1)), `NObs`, `ConvergedIters` (+ weighted `SumWeights` / `NEff`).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- A coefficient is the log odds of ITS level against the reference; e^β is a relative risk ratio.
- A level no fitted row carries is dropped (multinom's "group is empty"); a named `reference` with no rows fails the slot alone (`PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `reason: reference_empty`), fewer than two fitted levels too (`one_category`).
- Refused (`PROCESSING_CONFIG`, `details.requires`), predict too: `family` / `link` / `theta` / `dispersion`, `penalty`, `bounds`, `selection`, `resample`, a prior, `solver`, `intercept: false`, a robust `covariance`, `margins`, `collinearity`, `save`; `holdout` → `PULSE_MODEL_SCORE_INVALID` (no scorer); a `fits` entry → `PULSE_FIT_INVALID` (`regression_type`).
- Probability weight: multinom on w* = w·n_eff/Σw; no `TwoLogLik` / `AIC`.
- Separation → `PROCESSING_REGRESSION_SEPARATION`.

## See

- `pulse_examples_search tags=[multinomial]`
- Skills: `regression-spec`, `regression-modeling`, `regression-inference`
