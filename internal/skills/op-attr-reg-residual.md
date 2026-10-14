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

<!-- generated: use-when -->

## Params

| Name | Type | Description |
|---|---|---|
| `Target` | string | Dependent field (required). |
| `Predictors` | []string | Independent fields (required). |
| `Penalty` | enum | `""`, `l1`, `l2`, `elasticnet`. |
| `Alpha` | float | Regularization strength. |
| `L1Ratio` | float | Elasticnet mix. |
| `weight` | slot weight | `null` opts out; REG_OLS's kinds: WLS refit; residual raw y − ŷ. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Target` / `Predictors` | numeric analytics set: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |
| `Label` | required — new column name |

## Output

One `float64` per record — `yᵢ − ŷᵢ`; **null (never 0) when the target or any predictor is null**, so a downstream aggregation counts the row in `n_null`. With an intercept (always present for OLS) Σ residuals ≈ 0 across the fit set.

<!-- generated: reading-the-output -->

## Gotchas

- Two-pass: runs an independent fit per slot.
- Large residuals flag response-space outliers.
- Penalized residuals bias-shrunk; for diagnostics prefer unpenalized.

## See

- `pulse_examples_search tags=[regression, outlier-detection]`
- Skills: `regression-modeling`, `attribute-composition`, `op-attr-reg-fitted`
