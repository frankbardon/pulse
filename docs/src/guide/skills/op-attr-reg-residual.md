```yaml
name: op-attr-reg-residual
description: Per-row residual yᵢ − ŷᵢ from an OLS refit during the attribute prepass.
kind: operator
category: ATTR
operator: ATTR_REG_RESIDUAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
```

Row-level scalars; no `Response.Components`.

## Use when

Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it.

Questions it answers:

- Which stores sell far more or less than their size and footfall predict?
- Do the model's misses grow with the predicted value, or bend in a curve?

Use something else:

- `REG_OLS` when you want the model's summary (coefficients, R-squared).

## Params

Refit keys are top-level, not `params`; `fit` is `params.fit` ([`regression-row-scores`](regression-row-scores.md)).

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Regularization strength. |
| `l1_ratio` | float | — | Elasticnet mix. |
| `fit` | string | — | Score a bound fit (`fits` / `models` entry) instead of refitting: `y − score`, y the fit's target (a GLM's response residual); no refit keys beside it. |
| `kind` | enum | `response` | With `fit`: `pearson`, `deviance`, `working`. |
| `weight` | slot weight | inherited | `null` opts out; a WLS refit; ignored with `fit`. |

## Inputs

`target` / `predictors`: see `## Contract` (`decimal128` as its float64 echo, `packed_bool` 0/1).

- `Label` — new column name; default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>`

## Output

One `float64` per record — `yᵢ − ŷᵢ`; **null (never 0) when the target or any predictor is null, NaN or ±Inf**, so a downstream aggregation counts the row in `n_null`. With an intercept Σ residuals ≈ 0.

## Reading the output

- `value`: Actual minus predicted: the row's target value minus the value the straight-line model predicts for it, in the target's units. Over the rows used in an unpenalized fit the residuals average 0.
  - Sign: positive means the row's target is above what the model predicts from its predictors; negative means the row's target is below what the model predicts from its predictors.
  - Caveat: A residual is what this model leaves unexplained, not a measurement error and not the effect of any one thing left out: another set of predictors gives other residuals.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable.

## Gotchas

- Two-pass: each slot refits on its own. With `params.fit` it is row-local and fits nothing; refusals and a failed fit (`PULSE_FIT_UNAVAILABLE`, every row null): [`regression-row-scores`](regression-row-scores.md).
- Penalized residuals are bias-shrunk; diagnose on unpenalized fits. `kind` scales: [`regression-row-scores`](regression-row-scores.md).

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: [`regression-row-scores`](regression-row-scores.md), [`regression-modeling`](regression-modeling.md), [`attribute-composition`](attribute-composition.md), [`op-attr-reg-fitted`](op-attr-reg-fitted.md)
