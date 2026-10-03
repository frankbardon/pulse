---
name: op-attr-reg-residual
description: Per-row residual yᵢ − ŷᵢ from an OLS refit during the attribute prepass.
kind: operator
category: ATTR
operator: ATTR_REG_RESIDUAL
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Params

| Name | Type | Description |
|---|---|---|
| `Target` | string | Dependent field (required). |
| `Predictors` | []string | Independent fields (required). |
| `Penalty` | enum | `""`, `l1`, `l2`, `elasticnet`. |
| `Alpha` | float | Regularization strength. |
| `L1Ratio` | float | Elasticnet mix. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Target` / `Predictors` | numeric (no `decimal128`) |
| `Label` | required — new column name |

## Output

One `float64` per record — `yᵢ − ŷᵢ`. With an intercept (always present for OLS) Σ residuals ≈ 0 across the fit set.

## Gotchas

- Two-pass: runs an independent fit per slot.
- Large residuals flag response-space outliers.
- Penalized residuals bias-shrunk; for diagnostics prefer unpenalized.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-fitted`
