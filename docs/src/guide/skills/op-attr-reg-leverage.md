```yaml
name: op-attr-reg-leverage
description: Per-row hat-matrix diagonal hᵢᵢ from an unpenalized OLS refit — leverage diagnostic.
kind: operator
category: ATTR
operator: ATTR_REG_LEVERAGE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row its leverage: how unusual its predictor values are, and so how hard it can pull a straight-line fit.

Questions it answers:

- Which rows have predictor values so far from the rest that they could steer the model?
- Which customers have predictor values extreme enough that they could pull the fitted line (read their residuals to see whether they do)?

Use something else:

- `REG_OLS` when you want the model itself.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Target` | string | (required) | Dependent variable field. |
| `Predictors` | []string | (required) | Independent variable fields. |
| `weight` | slot weight | inherited | `null` opts out; REG_OLS kinds. Diag of W½X(XᵀWX)⁻¹XᵀW½ (R `hatvalues`); excluded row: 0. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Target` / `Predictors` | numeric (no `decimal128`) |
| `Label` | required — new column name |

## Output

One `float64` per record in `[0, 1]` — `hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)`. Sum across the fit set equals `p + 1` (predictors + intercept).

## Reading the output

- `value`: How far the row's predictor values sit from the other rows' (from the predictors' means, scaled by their spread and correlation), and so how hard the row can pull the fitted line toward itself: the hat-matrix diagonal 1/n + (x - mean)' M^-1 (x - mean), where M is the predictors' centred sum-of-squares-and-cross-products matrix. Over the n rows in the fit it runs from 1/n to 1 and averages p / n, where p counts the coefficients including the intercept.
  - Caveat: Unpenalized least squares only; a request with a penalty is refused.

## Gotchas

- **Unpenalized OLS only** — any non-empty `Penalty` raises `PROCESSING_CONFIG`.
- High leverage flags outliers in PREDICTOR space (vs residuals, which flag the response). Rule of thumb: hᵢᵢ > 2(p+1)/n.
- Two-pass — pre-pass fits OLS, pass 2 emits per row.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: [`regression-modeling`](regression-modeling.md), [`attribute-composition`](attribute-composition.md), [`op-attr-reg-residual`](op-attr-reg-residual.md)
