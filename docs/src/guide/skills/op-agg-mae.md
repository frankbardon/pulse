```yaml
name: op-agg-mae
description: Mean absolute error of a prediction against an actual — mean(|y - prediction|).
kind: operator
category: AGG
operator: AGG_MAE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, compose]
```

## Use when

Average size of a prediction's miss in the actual's own units, comparing an actual column with a prediction column row by row.

Questions it answers:

- On average, how many units off is the sales model on the held-out weeks?
- Is the forecast miss larger for some stores than for others?

Use something else:

- `AGG_RMSE` when large misses should count for more than small ones.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `prediction_field` | field | (required) | The predicted column (typically an `ATTR_REG_PREDICT` score column). |

Missing is `PROCESSING_CONFIG`; a non-numeric column on either side (categorical, set, date, datetime) `PULSE_FIELD_TYPE_UNSUPPORTED`, a `decimal128` actual `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`; an unknown name `SERVICE_VALIDATION` — predict and runtime alike.

Weight-aware: Σw·|y − ŷ| / Σw. A `decimal128` side under a weight → `PULSE_WEIGHT_UNSUPPORTED`.

## Inputs

`Field` is the actual: u4–u64, f32/f64, packed_bool. No train/holdout param — split with a filter or `GROUP_RANGE` range grouper.

## Output

Scalar `float64` in the actual's units. Rows with a null on either side are skipped; none left → NaN (`null`).

## Reading the output

- `value`: The average size of the miss between prediction and actual, ignoring sign, in the actual's own units. 0 means every prediction is exact.
  - Caveat: It says how big the misses are, not which way: a model that is always too high and one that is always too low can share a value.
  - Caveat: It has the actual's units, so compare it only between models scored on the same rows and target.

## Components

- `loss_sum` (Σw·|y − ŷ|), `weight_sum`, `n_scored`, `n_null_prediction`, `mae`.
- The `n` / `n_null` floor counts `field` only.

## Contract

- Types (`field`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Types (`prediction_field`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `loss_sum`, `weight_sum`, `n_scored`, `n_null_prediction`, `mae`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Sign-blind: a model always too high and one always too low can share a value.

## See

- `pulse_examples_search tags=[regression, compose]`
- Skills: [`op-agg-rmse`](op-agg-rmse.md), [`op-agg-mape`](op-agg-mape.md), [`op-attr-reg-predict`](op-attr-reg-predict.md)
