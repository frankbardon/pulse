---
name: op-win-lag
description: Per-row value of Field from N rows earlier in the ordered partition.
kind: operator
category: WIN
operator: WIN_LAG
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `offset` | int | `1` | Lookback distance (≥ 0). |
| `default` | any | `null` | Substitute when offset crosses partition start. |

`partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (forbidden).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no `packed_bool`, no categorical) |

## Output

One `float64` per row written to `Label` (default `WIN_LAG_<field>`). When `i - offset < 0` within the partition emits `default` if set, else `null`.

## Gotchas

- `order_by` required — predict rejects empty slate (`PULSE_WINDOW_INVALID`).
- `frame` forbidden — set one and predict rejects.
- Partitioning by the raw `date` collapses each row to its own partition. Pick a coarser partition (region, account) and order by date.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-lead`, `op-win-pct-change`
