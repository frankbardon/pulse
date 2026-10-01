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

## Params

None operator-level. `partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (REQUIRED — mode `"rows"`; typical `{preceding: null, following: 0}` for cumulative-to-current-row). `field` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no `packed_bool`, no categorical) |

## Output

One `float64` per row written to `Label` (default `WIN_RUNNING_AVG_<field>`). Arithmetic mean of non-null values inside the resolved frame. Empty slice → `null`.

## Gotchas

- Mechanically identical to `WIN_MOVING_AVG`; differentiator is FRAME — `MOVING_AVG` requires bounded both sides, `RUNNING_AVG` accepts unbounded preceding (cumulative).
- Nulls skipped (not zero-filled); denominator is non-null count.
- Result rows are NOT reordered — use `Request.Sort` for response order.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-moving-avg`, `op-win-running-sum`
