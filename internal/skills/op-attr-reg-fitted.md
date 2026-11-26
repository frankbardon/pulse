---
name: op-attr-reg-fitted
description: Per-row fitted value ŷᵢ = Xᵢ · β + β₀ from an OLS refit during the attribute prepass.
kind: operator
category: ATTR
operator: ATTR_REG_FITTED
type: reference
applies_to: process, compose, predict
examples_tags: [regression, ols, buffered-pipeline]
---

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
| `fit` | string | — | Bound fit (`fits` / `models`), no refit: its score (a GLM's fitted mean); no refit keys. |
| `carryover` | bool | `true` | Carry adstock (`regression-order`). |
| `order` | object | — | `{field, partition?}`; needed to carry. |
| `weight` | slot weight | inherited | `null` opts out; a WLS refit; ignored with `fit`. |

## Inputs

`target` / `predictors`: see `## Contract` (`decimal128` as its float64 echo, `packed_bool` 0/1).

- `label` — default `<TYPE>_<target>`, with `params.fit` `<TYPE>_<fit>`

## Output

One `float64` per record — the model's prediction ŷᵢ. **Null (never 0) when any predictor is null, NaN or ±Inf**, so a downstream aggregation counts the row in `n_null`; a null target alone still gets ŷᵢ.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Two-pass: each slot refits on its own. With `params.fit` row-local (two-pass with `order`), the bound model's score bit for bit; refusals, failed fit (`PULSE_FIT_UNAVAILABLE`, all null): `regression-row-scores`.
- A penalty shrinks coefficients toward zero when `alpha` / `l1_ratio` is mis-tuned.

## See

- `pulse_examples_search tags=[regression]`
- Skills: `regression-row-scores`, `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`
