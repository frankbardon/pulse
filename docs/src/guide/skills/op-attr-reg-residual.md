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

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it.

Questions it answers:

- Which stores sell far more or less than their size and footfall predict?
- Do the model's misses grow with the predicted value, or bend in a curve?

Use something else:

- `REG_OLS` when you want the model's summary (coefficients, R-squared).

## Params

Top-level keys, not `params` — except `params.fit`; `field` unread.

| Name | Type | Description |
|---|---|---|
| `target` | field | Dependent field (required without `params.fit`). |
| `predictors` | list | Independent fields (required without `params.fit`). |
| `penalty` | enum | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | Regularization strength. |
| `l1_ratio` | float | Elasticnet mix. |
| `weight` | slot weight | `null` opts out; REG_OLS's kinds: WLS refit; residual raw y − ŷ. With `params.fit` skipped (the fit's own weight applied when it ran). |
| `params.fit` | string | Score a bound fit (`fits` / `models` entry) instead of refitting: `y − score`, y the fit's target; a REG_GLM's response residual. No `target` / `predictors` / `penalty` / `alpha` / `l1_ratio` beside it. |

## Inputs

`target` / `predictors` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — new column name; default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>`

## Output

One `float64` per record — `yᵢ − ŷᵢ`; **null (never 0) when the target or any predictor is null**, so a downstream aggregation counts the row in `n_null`. With an intercept (always present for OLS) Σ residuals ≈ 0 across the fit set.

## Reading the output

- `value`: Actual minus predicted: the row's target value minus the value the straight-line model predicts for it, in the target's units. Over the rows used in an unpenalized fit the residuals average 0.
  - Sign: positive means the row's target is above what the model predicts from its predictors; negative means the row's target is below what the model predicts from its predictors.
  - Caveat: A residual is what this model leaves unexplained, not a measurement error and not the effect of any one thing left out: another set of predictors gives other residuals.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable.

## Gotchas

- Two-pass: runs an independent OLS fit per slot. With `params.fit` it is row-local (streams beside groupers, merges) and fits nothing.
- `params.fit` follows the scoring rule: an unknown name is `PULSE_MODEL_UNKNOWN`; an extension fit whose registration has no Scorer is `PULSE_MODEL_SCORE_INVALID` `no_scorer`; a refit field beside it is `slot_fields`; a failed fit nulls every row with `PULSE_FIT_UNAVAILABLE`. Predict refuses identically.
- Large residuals flag response-space outliers.
- Penalized residuals bias-shrunk; for diagnostics prefer unpenalized.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: [`regression-modeling`](regression-modeling.md), [`attribute-composition`](attribute-composition.md), [`op-attr-reg-fitted`](op-attr-reg-fitted.md)
