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
| `default` | any | — | Substitute when offset crosses partition end; absent: `null`. |

`partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (forbidden).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One value per row written to `Label` (default `WIN_LEAD_<field>`): a verbatim copy, so a `u64` id renders its exact word. When `i + offset >= partition_len` emits `default` if set, else `null`.

<!-- generated: contract -->

## Gotchas

- Mirror of a lag — same partition / order / frame rules, opposite scan direction.
- `order_by` required, `frame` forbidden.
- Result rows are NOT reordered by `order_by` — use `Request.Sort` for response order.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-lag`, `op-win-pct-change`
