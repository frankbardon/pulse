```yaml
name: op-attr-reg-leverage
description: Per-row hat-matrix diagonal hᵢᵢ from an OLS refit (any penalty) or a bound fit's design — leverage diagnostic.
kind: operator
category: ATTR
operator: ATTR_REG_LEVERAGE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
```

Row-level scalars; no `Response.Components`.

## Use when

Adds to every row its leverage: how unusual its predictor values are, and so how hard it can pull a straight-line fit.

Questions it answers:

- Which rows have predictor values so far from the rest that they could steer the model?
- Which customers have predictor values extreme enough that they could pull the fitted line (read their residuals to see whether they do)?

Use something else:

- `REG_OLS` when you want the model itself.

## Params

Refit keys are top-level, not `params`; `field` unread.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Regularization strength. |
| `l1_ratio` | float | — | Elasticnet mix. |
| `fit` | string | — | `params.fit`: a bound fit's leverage, no refit. |
| `carryover` | bool | `true` | Carry adstock ([`regression-order`](regression-order.md)). |
| `order` | object | — | `{field, partition?}`; needed to carry. |
| `weight` | slot weight | inherited | `null` opts out; diag of W½X(XᵀWX)⁻¹XᵀW½; excluded row: 0. Ignored with `fit`. |

## Inputs

`target` / `predictors`: `## Contract` (`decimal128` its float64 echo, `packed_bool` 0/1).

- `Label` — required — new column name

## Output

One `float64` per record — `hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)`. In `[1/n, 1]` over the fit's rows, summing to `p + 1`; a row outside the fit can exceed 1. Penalized: the ridge or active-set hat; with `fit`: the bound fit's design (both [`regression-influence`](regression-influence.md)). **Null (never 0) when any predictor is null, NaN or ±Inf**; a row the weighted refit excludes stays 0.

## Reading the output

- `value`: How far the row's predictor values sit from the other rows' (from the predictors' means, scaled by their spread and correlation), and so how hard the row can pull the fitted line toward itself: the hat-matrix diagonal 1/n + (x - mean)' M^-1 (x - mean), where M is the predictors' centred sum-of-squares-and-cross-products matrix. Over the n rows in the fit it runs from 1/n to 1 and averages p / n, where p counts the coefficients including the intercept.

## Contract

- Types (`target`, `predictor`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable.

## Gotchas

- Refit: two-pass. `fit`: row-local (two-pass with `order`), unpenalized.
- High leverage flags outliers in PREDICTOR space (residuals flag the response). Rule of thumb: hᵢᵢ > 2(p+1)/n.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: [`regression-influence`](regression-influence.md), [`regression-modeling`](regression-modeling.md), [`attribute-composition`](attribute-composition.md), [`op-attr-reg-residual`](op-attr-reg-residual.md), [`op-attr-reg-influence`](op-attr-reg-influence.md)
