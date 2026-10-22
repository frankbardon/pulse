---
name: op-agg-mape
description: Mean absolute percentage error of a prediction against an actual — mean(|y - prediction| / |y|), as a fraction.
kind: operator
category: AGG
operator: AGG_MAPE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, compose]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `prediction_field` | field | (required) | The predicted column (typically<!-- feature: ATTR_REG_PREDICT --> an `ATTR_REG_PREDICT`<!-- /feature --> score column). |

Missing is `PROCESSING_CONFIG`; a non-numeric column on either side (categorical, set, date, datetime) `PULSE_FIELD_TYPE_UNSUPPORTED`, a `decimal128` actual `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`; an unknown name `SERVICE_VALIDATION` — predict and runtime alike.

<!-- feature: capability:weighting -->Weight-aware: Σw·loss / Σw. A `decimal128` side under a weight → `PULSE_WEIGHT_UNSUPPORTED`.<!-- /feature -->

## Inputs

`Field` is the actual: u4–u64, f32/f64, packed_bool. No train/holdout param — split with a filter or<!-- feature: GROUP_RANGE --> `GROUP_RANGE`<!-- /feature --> range grouper.

## Output

Scalar `float64` FRACTION (0.12 = 12%). Rows with a null on either side, or an actual of exactly 0, are skipped; none left → NaN (`null`).

<!-- generated: reading-the-output -->

## Components

- `loss_sum`, `weight_sum`, `n_scored`, `n_null_prediction`, `n_zero_actual` (zero actuals skipped), `mape`.
- The `n` / `n_null` floor counts `field` only.

<!-- generated: contract -->

## Gotchas

- Not ×100. Near-zero actuals still inflate it; prefer<!-- feature: AGG_MAE --> `AGG_MAE`<!-- /feature --> there.

## See

- `pulse_examples_search tags=[regression, compose]`
- Skills: `op-agg-rmse`, `op-agg-mae`, `op-attr-reg-predict`
