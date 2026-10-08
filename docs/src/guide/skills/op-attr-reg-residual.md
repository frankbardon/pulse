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

| Name | Type | Description |
|---|---|---|
| `Target` | string | Dependent field (required). |
| `Predictors` | []string | Independent fields (required). |
| `Penalty` | enum | `""`, `l1`, `l2`, `elasticnet`. |
| `Alpha` | float | Regularization strength. |
| `L1Ratio` | float | Elasticnet mix. |
| `weight` | slot weight | `null` opts out; REG_OLS's kinds: WLS refit; residual raw y − ŷ. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Target` / `Predictors` | numeric (no `decimal128`) |
| `Label` | required — new column name |

## Output

One `float64` per record — `yᵢ − ŷᵢ`. With an intercept (always present for OLS) Σ residuals ≈ 0 across the fit set.

## Reading the output

- `value`: Actual minus predicted: the row's target value minus the value the straight-line model predicts for it, in the target's units. Over the rows used in an unpenalized fit the residuals average 0.
  - Sign: positive means the row's target is above what the model predicts from its predictors; negative means the row's target is below what the model predicts from its predictors.
  - Caveat: A residual is what this model leaves unexplained, not a measurement error and not the effect of any one thing left out: another set of predictors gives other residuals.

## Gotchas

- Two-pass: runs an independent fit per slot.
- Large residuals flag response-space outliers.
- Penalized residuals bias-shrunk; for diagnostics prefer unpenalized.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: [`regression-modeling`](regression-modeling.md), [`attribute-composition`](attribute-composition.md), [`op-attr-reg-fitted`](op-attr-reg-fitted.md)
