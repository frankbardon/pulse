```yaml
name: op-reg-multinomial
description: Multinomial (baseline-category) logistic regression of a categorical target on numeric predictors; one coefficient set per non-reference level, log odds against the reference. Always buffered.
kind: operator
category: REG
operator: REG_MULTINOMIAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, multinomial, buffered-pipeline]
```

Fits ride `Response.Regressions[i]`; no `Response.Components`.

## Use when

Fits a model of an outcome with three or more categories (which plan, brand or channel), comparing each with a reference category.

Questions it answers:

- Which customer traits are associated with choosing the premium or the basic plan over the standard one, holding the others fixed?
- How does the chance of each support channel being picked change with the customer's age?

## Params

Flat keys (a `params` key is `PULSE_REQUEST_UNKNOWN_FIELD`); shared keys: [`regression-spec`](regression-spec.md).

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
| `multiplicity` | object | — | Default family: every non-intercept key; `coefficients` lists `<level>:<term>` keys. |
| `name` | string | — | Result name. |

## Inputs

`target`: categorical (`categorical_u8/u16/u32`); a numeric target is `PULSE_FIELD_TYPE_UNSUPPORTED`, a derived column `PROCESSING_CONFIG` (`requires: categorical_target`). `predictors`: see `## Contract`; a null, NaN or ±Inf drops the row from `n_obs`.

## Output

`RegressionResult`: `Reference`, `Categories` (fitted levels, dictionary order); `Coefficients` / `StdErrors` / `PValues` (Wald z) / `CILower` / `CIUpper` keyed `"<level>:<term>"` per non-reference level, the intercept `"<level>:(intercept)"` (nnet's vcov names); `Deviance` (−2ℓ), `NullDeviance`, `PseudoR2` (McFadden), `TwoLogLik`, `AIC` (+ 2·K·(p+1)), `NObs`, `ConvergedIters` (+ weighted `SumWeights` / `NEff`).

## Reading the output

- `coefficients.*`: Keyed &lt;category&gt;:&lt;term&gt;: the change in the log odds of that category against the reference category for a one-unit increase in the term's predictor, holding the other predictors fixed; &lt;category&gt;:(intercept) is that log odds when every predictor is 0.
- `std_errors.*`: How much the coefficient would vary from sample to sample, from the inverse of the fitted information matrix (nnet::multinom's Hessian).
- `conf_level`: The coverage ci_lower and ci_upper were built at (the request's conf_level, 0.95 by default).

## Contract

- Types (`target`): `categorical_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Types (`predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Multiplicity: its own `multiplicity` block (an inherited one never reaches it) adds per-coefficient `p_adjusted` and `significant_adjusted` beside the raw p; families `request` (default), `compose` (inside Compose).
- Streaming: buffered only. Always buffered; Newton-Raphson requires multiple passes over the data.

## Gotchas

- A coefficient is the log odds of ITS level against the reference; e^β is a relative risk ratio.
- A level no fitted row carries is dropped (multinom's "group is empty"); a named `reference` with no rows fails the slot alone (`PROCESSING_REGRESSION_INSUFFICIENT_DATA`, `reason: reference_empty`), fewer than two fitted levels too (`one_category`).
- Refused (`PROCESSING_CONFIG`, `details.requires`), predict too: `family` / `link` / `theta` / `dispersion`, `penalty`, `bounds`, `selection`, `resample`, a prior, `solver`, `intercept: false`, a robust `covariance`, `margins`, `collinearity`, `save`; `holdout` → `PULSE_MODEL_SCORE_INVALID` (no scorer); a `fits` entry → `PULSE_FIT_INVALID` (`regression_type`).
- Probability weight: multinom on w* = w·n_eff/Σw; no `TwoLogLik` / `AIC`.
- Separation → `PROCESSING_REGRESSION_SEPARATION`.

## See

- `pulse_examples_search tags=[multinomial]`
- Skills: [`regression-spec`](regression-spec.md), [`regression-modeling`](regression-modeling.md), [`regression-inference`](regression-inference.md)
