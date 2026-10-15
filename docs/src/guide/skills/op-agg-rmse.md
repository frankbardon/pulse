```yaml
name: op-agg-rmse
description: Root mean squared error of a prediction against an actual — sqrt(mean((y - prediction)^2)).
kind: operator
category: AGG
operator: AGG_RMSE
type: reference
applies_to: process, compose, predict
examples_tags: [regression, compose]
```

## Use when

Typical size of a prediction's miss in the actual's own units, with large misses counting more: the root of the average squared miss.

Questions it answers:

- How many units off is the sales model on the held-out weeks, with big misses weighing more?
- Which of two models misses less on the same rows?

Use something else:

- `AGG_MAE` when every unit of miss should count the same, so a few large misses do not dominate.

## Params

`prediction_field` — required; the predicted column (typically an `ATTR_REG_PREDICT` score column). Missing is `PROCESSING_CONFIG`; a non-numeric column on either side (categorical, set, date, datetime) `PULSE_FIELD_TYPE_UNSUPPORTED`, a `decimal128` actual `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`; an unknown name `SERVICE_VALIDATION` — predict and runtime alike.

Weight-aware: √(Σw·(y − ŷ)² / Σw), the root of the pooled MSE. A `decimal128` side under a weight → `PULSE_WEIGHT_UNSUPPORTED`.

## Inputs

`Field` is the actual: u4–u64, f32/f64, packed_bool. No train/holdout param — split with a filter or `GROUP_RANGE` range grouper.

## Output

Scalar `float64` in the actual's units. Rows with a null on either side are skipped; none left → NaN (`null`).

## Reading the output

- `value`: The square root of the average squared miss between prediction and actual, in the actual's own units. 0 means every prediction is exact; it is never below AGG_MAE on the same rows.
  - Caveat: Squaring makes a few large misses dominate it; a gap well above AGG_MAE on the same rows says the misses are uneven.
  - Caveat: It has the actual's units, so compare it only between models scored on the same rows and target.

## Components

Floor `{n, n_null}` (on `field` only) (weighted adds `sum_weights`, `n_eff`, `n_weight_invalid`) plus `loss_sum` (Σw·(y − ŷ)²), `weight_sum`, `n_scored`, `n_null_prediction`, `mse`, `rmse`. `Mergeable` (pooled sums, root at finalize); streams.

## Gotchas

- Squaring lets a few large misses dominate; always ≥ `AGG_MAE` on the same rows.

## See

- `pulse_examples_search tags=[regression, compose]`
- Skills: [`op-agg-mae`](op-agg-mae.md), [`op-agg-mape`](op-agg-mape.md), [`op-attr-reg-predict`](op-attr-reg-predict.md)
