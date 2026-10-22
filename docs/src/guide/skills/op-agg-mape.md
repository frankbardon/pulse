```yaml
name: op-agg-mape
description: Mean absolute percentage error of a prediction against an actual — mean(|y - prediction| / |y|), as a fraction.
kind: operator
category: AGG
operator: AGG_MAPE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, compose]
```

## Use when

Average size of a prediction's miss as a share of the actual value, comparing an actual column with a prediction column row by row.

Questions it answers:

- On the held-out weeks, how far off is the sales model, as a share of actual sales?
- Is the forecast's percentage miss larger in some regions than in others?

Use something else:

- `AGG_MAE` when actual values can be 0 or close to 0, where a share is undefined or explodes.

## Params

`prediction_field` — required; the predicted column (typically an `ATTR_REG_PREDICT` score column). Missing is `PROCESSING_CONFIG`; a non-numeric column on either side (categorical, set, date, datetime) `PULSE_FIELD_TYPE_UNSUPPORTED`, a `decimal128` actual `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`; an unknown name `SERVICE_VALIDATION` — predict and runtime alike.

Weight-aware: Σw·loss / Σw. A `decimal128` side under a weight → `PULSE_WEIGHT_UNSUPPORTED`.

## Inputs

`Field` is the actual: u4–u64, f32/f64, packed_bool. No train/holdout param — split with a filter or `GROUP_RANGE` range grouper.

## Output

Scalar `float64` FRACTION (0.12 = 12%). Rows with a null on either side, or an actual of exactly 0, are skipped; none left → NaN (`null`).

## Reading the output

- `value`: The average of each row's miss divided by its actual value, ignoring sign, as a FRACTION: 0.12 means the predictions are off by 12% of the actual value on average. 0 means every prediction is exact.
  - Caveat: Rows whose actual is exactly 0 are left out (their share is undefined) and counted in components n_zero_actual; actual values near 0 can still inflate it sharply.
  - Caveat: It punishes over-prediction more than under-prediction of the same size, because the share is taken of the actual.

## Components

- `loss_sum`, `weight_sum`, `n_scored`, `n_null_prediction`, `n_zero_actual` (zero actuals skipped), `mape`.
- The `n` / `n_null` floor counts `field` only.

## Contract

- Types (`field`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Types (`prediction_field`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `loss_sum`, `weight_sum`, `n_scored`, `n_null_prediction`, `n_zero_actual`, `mape`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Not ×100. Near-zero actuals still inflate it; prefer `AGG_MAE` there.

## See

- `pulse_examples_search tags=[regression, compose]`
- Skills: [`op-agg-rmse`](op-agg-rmse.md), [`op-agg-mae`](op-agg-mae.md), [`op-attr-reg-predict`](op-attr-reg-predict.md)
