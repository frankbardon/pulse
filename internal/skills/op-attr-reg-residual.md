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

Refit keys are top-level; `fit` is `params.fit` (`regression-row-scores`).

| Name | Type | Default | Description |
|---|---|---|---|
| `target` | field | (required) | Dependent field; absent with `fit`. |
| `predictors` | list | (required) | Independent fields; absent with `fit`. |
| `penalty` | enum | `""` | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | — | Strength. |
| `l1_ratio` | float | — | L1 mix. |
| `fit` | string | — | Bound fit (`fits` / `models`), no refit: `y − score`. |
| `kind` | enum | `response` | `pearson`, `deviance`, `working`, `standardized`, `studentized` (`fit` needs `vcov`). |
| `residual_type` | enum | `pearson` | kind `standardized` only; or `deviance`. |
| `carryover` | bool | `true` | Carry adstock (`regression-order`). |
| `order` | object | — | `{field, partition?}`; needed to carry. |
| `weight` | slot weight | inherited | `null` opts out; WLS refit; not with `fit`. |

## Inputs

`target` / `predictors`: see `## Contract` (`decimal128` as its float64 echo, `packed_bool` 0/1).

- `Label` — default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>`

## Output

One `float64` per record — `yᵢ − ŷᵢ`; **null (never 0) when the target or any predictor is null, NaN or ±Inf** (counted in `n_null`). With an intercept Σ ≈ 0.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Two-pass: refits per slot. With `params.fit` row-local (two-pass with `order`), no refit; refusals, failed fit (all null): `regression-row-scores`.
- Penalized residuals are shrunk; diagnose unpenalized. `kind` rules: `regression-row-scores`.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-row-scores`, `regression-modeling`, `attribute-composition`, `op-attr-reg-fitted`
