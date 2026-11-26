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

<!-- generated: use-when -->

## Params

Refit keys top-level, not `params`; `field` unread.

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Regularization strength. |
| `l1_ratio` | float | — | Elasticnet mix. |
<!-- feature: REG_GLM -->
| `family` | enum | — | `params.family`: refit a GLM. |
| `link` | enum | — | `params.link`; default canonical. |
<!-- /feature -->
| `fit` | string | — | `params.fit`: a bound fit's leverage. |
| `carryover` | bool | `true` | Carry adstock (`regression-order`). |
| `order` | object | — | `{field, partition?}`; needed to carry. |
| `weight` | slot weight | inherited | `null` opts out; ignored with `fit`. |

## Inputs

`target` / `predictors`: see `## Contract`.

- `label` — default `<TYPE>_<target>` (`<TYPE>_<fit>` with `fit`)

## Output

One `float64` per record — `hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀM2_xx⁻¹(xᵢ − μ_x)`. In `[1/n, 1]` over the fit's rows, summing to `p + 1`; a row outside the fit can exceed 1. Penalized: the ridge or active-set hat; `family`: the GLM hat; `fit`: the bound fit's design (all `regression-influence`). **Null (never 0) on a null, NaN or ±Inf predictor**; a row the weighted refit excludes stays 0.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Refit: two-pass. `fit`: row-local (two-pass with `order`), any penalty.
- Flags outliers in PREDICTOR space. Rule of thumb: hᵢᵢ > 2(p+1)/n; over a frequency-weighted `fit` h is one copy's, so /Σw.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-influence`, `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`, `op-attr-reg-influence`
