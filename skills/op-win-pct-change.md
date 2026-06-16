---
name: op-win-pct-change
description: Percent change relative to the row `periods` positions earlier in the ordered partition.
kind: operator
category: WIN
operator: WIN_PCT_CHANGE
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `periods` | int | `1` | Lookback distance (≥ 1) for the comparison row. |

`partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (forbidden). `field` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no `packed_bool`, no categorical) |

## Output

One `float64` per row written to `Label` (default `WIN_PCT_CHANGE_<field>`). Formula `(cur - prev) / prev` where `prev` is `periods` positions back. Rows `i < periods` within the partition emit `null`.

## Gotchas

- `periods <= 0` REJECTED at predict (`PULSE_WINDOW_INVALID`).
- `prev == 0` emits `null` (no panic, no `+Inf`).
- Either side null emits `null`.
- Result rows are NOT reordered — use `Request.Sort`.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-lag`, `overlay-system`
