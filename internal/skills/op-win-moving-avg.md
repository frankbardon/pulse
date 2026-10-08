---
name: op-win-moving-avg
description: Moving average of Field over a bounded Frame; both ends must be set.
kind: operator
category: WIN
operator: WIN_MOVING_AVG
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

None operator-level. `partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (REQUIRED — mode `"rows"`, `preceding` AND `following` BOTH bounded). `field` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no `packed_bool`, no categorical) |

## Output

One `float64` per row written to `Label` (default `WIN_MOVING_AVG_<field>`). Mean of non-null values inside `[i - preceding, i + following]` within the partition. Empty slice → `null`.

<!-- generated: reading-the-output -->

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- Unbounded frame on either end is REJECTED at predict.
- Trailing 7-row window: `frame: {mode: "rows", preceding: 6, following: 0}`.
- Nulls skipped (not zero-filled); denominator is non-null count.
- Result rows are NOT reordered — use `Request.Sort`.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-running-avg`, `op-win-ewma`
