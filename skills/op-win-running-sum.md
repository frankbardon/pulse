---
name: op-win-running-sum
description: Running total of Field over the configured Frame within the ordered partition.
kind: operator
category: WIN
operator: WIN_RUNNING_SUM
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

One `float64` per row written to `Label` (default `WIN_RUNNING_SUM_<field>`). Sum of non-null values inside the resolved frame within the partition. Empty slice → `null`.

## Gotchas

- Frame REQUIRED — `null` preceding = UNBOUNDED PRECEDING (full cumulative).
- Bounded frames produce a windowed sum, not cumulative — pair with `WIN_MOVING_AVG` for windowed mean.
- Nulls skipped (not zero-filled).
- Result rows are NOT reordered — use `Request.Sort`.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-running-avg`, `op-win-moving-avg`
