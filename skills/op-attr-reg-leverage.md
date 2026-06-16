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

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Target` | string | (required) | Dependent variable field. |
| `Predictors` | []string | (required) | Independent variable fields. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Target` / `Predictors` | numeric (no `decimal128`) |
| `Label` | required — new column name |

## Output

One `float64` per record in `[0, 1]` — `hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)`. Sum across the fit set equals `p + 1` (predictors + intercept).

## Gotchas

- **Unpenalized OLS only** — any non-empty `Penalty` raises `PROCESSING_CONFIG`. Penalized leverage / GLM leverage deferred.
- High leverage flags outliers in PREDICTOR space (vs residuals, which flag the response). Common rule of thumb: hᵢᵢ > 2(p+1)/n.
- Pair with `ATTR_REG_RESIDUAL` via Compose for Cook-style influence work.
- Two-pass — pre-pass fits OLS, pass 2 emits per row.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`
