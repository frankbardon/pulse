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

One `float64` per record — the model's prediction ŷᵢ. NaN-free over the filter-passing rows used to fit.

## Gotchas

- Two-pass: prepass refits independently per ATTR_REG_* slot (Option A — no fit sharing).
- `Mergeable` per-shard; predict reports streamability.
- Penalized fits (`l1`/`l2`/`elasticnet`) reuse the same machinery; mis-tuned `Alpha`/`L1Ratio` shrinks coefficients toward zero.
- For residual yᵢ − ŷᵢ use `ATTR_REG_RESIDUAL`; for leverage use `ATTR_REG_LEVERAGE`.

## See

- `pulse_examples_search tags=[regression]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-residual`
