---
name: op-attr-reg-leverage
description: Per-row hat-matrix diagonal hᵢᵢ from an unpenalized OLS refit — leverage diagnostic.
kind: operator
category: ATTR
operator: ATTR_REG_LEVERAGE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

Top-level keys, not `params`; `field` unread.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent variable field. |
| `predictors` | list | (required) | Independent variable fields. |
| `weight` | slot weight | inherited | `null` opts out; REG_OLS kinds. Diag of W½X(XᵀWX)⁻¹XᵀW½ (R `hatvalues`); excluded row: 0. |

## Inputs

`target` / `predictors` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — required — new column name

## Output

One `float64` per record — `hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)`. Over the rows in the fit it lies in `[1/n, 1]` and sums to `p + 1` (predictors + intercept); a row outside the fit (null target, predictors present) is scored from its predictors and can exceed 1. **Null (never 0) when any predictor is null**; a row the weighted refit excludes stays 0.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- **Unpenalized OLS only** — any non-empty `penalty` raises `PROCESSING_CONFIG`.
- High leverage flags outliers in PREDICTOR space (vs residuals, which flag the response). Rule of thumb: hᵢᵢ > 2(p+1)/n.
- Two-pass — pre-pass fits OLS, pass 2 emits per row.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`
