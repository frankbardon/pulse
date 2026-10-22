```yaml
name: op-win-running-avg
description: Running average of Field over the configured Frame within the ordered partition.
kind: operator
category: WIN
operator: WIN_RUNNING_AVG
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Adds the average of a field over the rows so far in the ordered series, such as the average order value to date.

Questions it answers:

- What is the average monthly revenue for the year so far, at each month?
- How has the cumulative average score settled as more waves came in?

Use something else:

- `WIN_MOVING_AVG` when you want the average of a fixed number of recent rows.

## Params

None operator-level. `partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (REQUIRED — mode `"rows"`; typical `{preceding: null, following: 0}` for cumulative-to-current-row). `field` (required, numeric).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One `float64` per row written to `Label` (default `WIN_RUNNING_AVG_<field>`). Arithmetic mean of non-null values inside the resolved frame. Empty slice → `null`.

## Reading the output

- `value`: The mean of the field over the rows in the frame. With the usual frame (no preceding bound, following 0) it is the cumulative average: the mean of every row from the partition's start up to and including this one.
  - Caveat: Early values rest on few rows and swing; later ones rest on the whole history and barely move, so a late cumulative average says little about the recent level (use WIN_MOVING_AVG for that).
  - Caveat: A frame with a following bound above 0, or none, takes in later rows too.

## Contract

- Types (`field`): `date`, `f32`, `f64`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only.

## Gotchas

- Mechanically identical to a moving average; the differentiator is FRAME — `MOVING_AVG` requires bounded both sides, `RUNNING_AVG` accepts unbounded preceding (cumulative). No frame → `PULSE_WINDOW_INVALID` (predict and runtime).
- Nulls skipped (not zero-filled); denominator is non-null count.
- Result rows are NOT reordered — use `Request.Sort` for response order.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-moving-avg`](op-win-moving-avg.md), [`op-win-running-sum`](op-win-running-sum.md)
