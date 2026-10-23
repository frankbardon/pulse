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

Row-level scalars; no `Response.Components`.

<!-- generated: use-when -->

## Params

Refit keys are top-level, not `params`; `fit` is `params.fit` (`regression-row-scores`).

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Regularization strength. |
| `l1_ratio` | float | — | Elasticnet mix. |
| `fit` | string | — | Score a bound fit (`fits` / `models` entry) instead of refitting: `y − score`, y the fit's target (a GLM's response residual); no refit keys beside it. |
| `weight` | slot weight | inherited | `null` opts out; a WLS refit; ignored with `fit`. |

## Inputs

`target` / `predictors`: see `## Contract` (`decimal128` as its float64 echo, `packed_bool` 0/1).

- `Label` — new column name; default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>`

## Output

One `float64` per record — `yᵢ − ŷᵢ`; **null (never 0) when the target or any predictor is null, NaN or ±Inf**, so a downstream aggregation counts the row in `n_null`. With an intercept (always present for OLS) Σ residuals ≈ 0 across the fit set.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Two-pass: each slot refits on its own. With `params.fit` it is row-local and fits nothing; refusals and a failed fit (`PULSE_FIT_UNAVAILABLE`, every row null): `regression-row-scores`.
- Large residuals flag response-space outliers. Penalized residuals are bias-shrunk; for diagnostics prefer unpenalized.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-row-scores`, `regression-modeling`, `attribute-composition`, `op-attr-reg-fitted`
