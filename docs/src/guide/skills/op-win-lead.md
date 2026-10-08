```yaml
name: op-win-lead
description: Per-row value of Field from N rows later in the ordered partition.
kind: operator
category: WIN
operator: WIN_LEAD
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Adds to every row the value of a field from a set number of rows later in the ordered series, such as next month's sales.

Questions it answers:

- What did each customer spend in the following month, beside this month's spend?
- What reading came next after each measurement?

Use something else:

- `WIN_LAG` when you want the value from an earlier row.
- `WIN_DELTA` when you want how much the field changed between rows.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `offset` | int | `1` | Lookahead distance (≥ 0). |
| `default` | any | `null` | Substitute when offset crosses partition end. |

`partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (forbidden).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no `packed_bool`, no categorical) |

## Output

One `float64` per row written to `Label` (default `WIN_LEAD_<field>`). When `i + offset >= partition_len` emits `default` if set, else `null`.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- Mirror of a lag — same partition / order / frame rules, opposite scan direction.
- `order_by` required, `frame` forbidden.
- Result rows are NOT reordered by `order_by` — use `Request.Sort` for response order.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-lag`](op-win-lag.md), [`op-win-pct-change`](op-win-pct-change.md)
