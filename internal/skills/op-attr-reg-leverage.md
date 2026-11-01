---
name: op-attr-reg-leverage
description: Per-row hat-matrix diagonal hᵢᵢ from an unpenalized OLS refit or a bound fit's design — leverage diagnostic.
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
| `fit` | string | — | `params.fit`: a bound fit's leverage, no refit (`regression-row-scores`). |
| `weight` | slot weight | inherited | `null` opts out; REG_OLS kinds. Diag of W½X(XᵀWX)⁻¹XᵀW½ (R `hatvalues`); excluded row: 0. Ignored with `fit`. |

## Inputs

`target` / `predictors`: see `## Contract` (`decimal128` as its float64 echo, `packed_bool` 0/1).

- `Label` — required — new column name

## Output

One `float64` per record — `hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)`. Over the rows in the fit it lies in `[1/n, 1]` and sums to `p + 1`; a row outside the fit is scored from its predictors and can exceed 1. With `fit`: w·x̃ᵀ(vcov/φ)x̃, w the GLM working weight (1 for OLS). **Null (never 0) when any predictor is null, NaN or ±Inf**; a row the weighted refit excludes stays 0.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Refit: unpenalized OLS only (`penalty` is `PROCESSING_CONFIG`); two-pass.
- `fit`: an unweighted, unpenalized OLS / GLM fit with `vcov: true`, else `PULSE_MODEL_SCORE_INVALID` (`details.requires`). Row-local.
- High leverage flags outliers in PREDICTOR space (residuals flag the response). Rule of thumb: hᵢᵢ > 2(p+1)/n.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`, `op-attr-reg-influence`
