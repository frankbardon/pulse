---
name: op-win-lead
description: Per-row value of Field from N rows later in the ordered partition.
kind: operator
category: WIN
operator: WIN_LEAD
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `offset` | int | `1` | Lookahead distance (≥ 0). |
| `default` | any | `null` | Substitute when offset crosses partition end. |

`partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (forbidden).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`; any other type (categorical, `set_*`, `decimal128`, `packed_bool`, `datetime`) is refused `PULSE_FIELD_TYPE_UNSUPPORTED` |

## Output

One value per row written to `Label` (default `WIN_LEAD_<field>`): a verbatim copy, so a `u64` id renders its exact word. When `i + offset >= partition_len` emits `default` if set, else `null`.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- Mirror of a lag — same partition / order / frame rules, opposite scan direction.
- `order_by` required, `frame` forbidden.
- Result rows are NOT reordered by `order_by` — use `Request.Sort` for response order.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-lag`, `op-win-pct-change`
