```yaml
name: op-win-lag
description: Per-row value of Field from N rows earlier in the ordered partition.
kind: operator
category: WIN
operator: WIN_LAG
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Adds to every row the value of a field from a set number of rows earlier in the ordered series, such as last month's sales.

Questions it answers:

- What were each store's sales in the month before, on the same row as this month's?
- What score did each respondent give in the previous wave?

Use something else:

- `WIN_DELTA` when you want the difference from the earlier row, not its value.

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

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- `order_by` required — predict rejects empty slate (`PULSE_WINDOW_INVALID`).
- `frame` forbidden — set one and predict rejects.
- Partitioning by the raw `date` collapses each row to its own partition. Pick a coarser partition (region, account) and order by date.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-lead`](op-win-lead.md), [`op-win-pct-change`](op-win-pct-change.md)
