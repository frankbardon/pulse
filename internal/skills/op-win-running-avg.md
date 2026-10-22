---
name: op-win-running-avg
description: Running average of Field over the configured Frame within the ordered partition.
kind: operator
category: WIN
operator: WIN_RUNNING_AVG
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

None operator-level. `partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (REQUIRED — mode `"rows"`; typical `{preceding: null, following: 0}` for cumulative-to-current-row). `field` (required, numeric).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One `float64` per row written to `Label` (default `WIN_RUNNING_AVG_<field>`). Arithmetic mean of non-null values inside the resolved frame. Empty slice → `null`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Mechanically identical to a moving average; the differentiator is FRAME — `MOVING_AVG` requires bounded both sides, `RUNNING_AVG` accepts unbounded preceding (cumulative). No frame → `PULSE_WINDOW_INVALID` (predict and runtime).
- Nulls skipped (not zero-filled); denominator is non-null count.
- Result rows are NOT reordered — use `Request.Sort` for response order.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-moving-avg`, `op-win-running-sum`
