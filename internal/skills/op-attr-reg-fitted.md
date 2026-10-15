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

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

Top-level keys, not `params`; `field` unread.

| Name | Type | Description |
|---|---|---|
| `target` | field | Dependent field (required). |
| `predictors` | list | Independent fields (required). |
| `penalty` | enum | `""`, `l1`, `l2`, `elasticnet`. |
| `alpha` | float | Regularization strength. |
| `l1_ratio` | float | Elasticnet mix. |
| `weight` | slot weight | `null` opts out; REG_OLS's kinds: WLS refit; residual raw y − ŷ. |

## Inputs

| Param | Accepted field types |
|---|---|
| `target` / `predictors` | numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |
| `Label` | required — new column name |

## Output

One `float64` per record — the model's prediction ŷᵢ. **Null (never 0) when any predictor is null**, so a downstream aggregation counts the row in `n_null`; a null target alone still gets ŷᵢ.

<!-- generated: reading-the-output -->

## Gotchas

- Two-pass: prepass refits independently per ATTR_REG_* slot (Option A — no fit sharing).
- `Mergeable` per-shard; predict reports streamability.
- Penalized fits (`l1`/`l2`/`elasticnet`) reuse the same machinery; mis-tuned `alpha`/`l1_ratio` shrinks coefficients toward zero.

## See

- `pulse_examples_search tags=[regression]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`
