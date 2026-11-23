---
name: op-attr-reg-leverage
description: Per-row hat-matrix diagonal hᵢᵢ from an OLS refit (any penalty) or a bound fit's design — leverage diagnostic.
kind: operator
category: ATTR
operator: ATTR_REG_LEVERAGE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, outlier-detection, buffered-pipeline]
---

Row-level scalars; no `Response.Components`.

<!-- generated: use-when -->

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
| `carryover` | bool | `true` | Carry adstock (`regression-order`). |
| `order` | object | — | `{field, partition?}`; needed to carry. |
| `weight` | slot weight | inherited | `null` opts out; diag of W½X(XᵀWX)⁻¹XᵀW½; excluded row: 0. Ignored with `fit`. |

## Inputs

`target` / `predictors`: `## Contract` (`decimal128` its float64 echo, `packed_bool` 0/1).

- `Label` — required — new column name

## Output

One `float64` per record — `hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)`. In `[1/n, 1]` over the fit's rows, summing to `p + 1`; a row outside the fit can exceed 1. Penalized: the ridge or active-set hat; with `fit`: the bound fit's design (both `regression-influence`). **Null (never 0) when any predictor is null, NaN or ±Inf**; a row the weighted refit excludes stays 0.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Refit: two-pass. `fit`: row-local (two-pass with `order`), unpenalized.
- High leverage flags outliers in PREDICTOR space (residuals flag the response). Rule of thumb: hᵢᵢ > 2(p+1)/n.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-influence`, `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`, `op-attr-reg-influence`
