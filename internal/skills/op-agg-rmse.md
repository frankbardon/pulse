---
name: op-agg-rmse
description: Root mean squared error of a prediction against an actual — sqrt(mean((y - prediction)^2)).
kind: operator
category: AGG
operator: AGG_RMSE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, compose]
---

<!-- generated: use-when -->

## Params

`prediction_field` — required; the predicted column (typically<!-- feature: ATTR_REG_PREDICT --> an `ATTR_REG_PREDICT`<!-- /feature --> score column). Missing is `PROCESSING_CONFIG`; a non-numeric column on either side (categorical, set, date, datetime) `PULSE_FIELD_TYPE_UNSUPPORTED`, a `decimal128` actual `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`; an unknown name `SERVICE_VALIDATION` — predict and runtime alike.

<!-- feature: capability:weighting -->Weight-aware: √(Σw·(y − ŷ)² / Σw), the root of the pooled MSE. A `decimal128` side under a weight → `PULSE_WEIGHT_UNSUPPORTED`.<!-- /feature -->

## Inputs

`Field` is the actual: u4–u64, f32/f64, packed_bool. No train/holdout param — split with a filter or<!-- feature: GROUP_RANGE --> `GROUP_RANGE`<!-- /feature --> range grouper.

## Output

Scalar `float64` in the actual's units. Rows with a null on either side are skipped; none left → NaN (`null`).

<!-- generated: reading-the-output -->

## Components

Floor `{n, n_null}` (on `field` only)<!-- feature: capability:weighting --> (weighted adds `sum_weights`, `n_eff`, `n_weight_invalid`)<!-- /feature --> plus `loss_sum` (Σw·(y − ŷ)²), `weight_sum`, `n_scored`, `n_null_prediction`, `mse`, `rmse`. `Mergeable` (pooled sums, root at finalize); streams.

## Gotchas

- Squaring lets a few large misses dominate; always ≥<!-- feature: AGG_MAE --> `AGG_MAE`<!-- /feature --> on the same rows.

## See

- `pulse_examples_search tags=[regression, compose]`
- Skills: `op-agg-mae`, `op-agg-mape`, `op-attr-reg-predict`
