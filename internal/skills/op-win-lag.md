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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `offset` | int | `1` | Lookback distance (≥ 0). |
| `default` | any | `null` | Substitute when offset crosses partition start. |

`partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (forbidden).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One value per row written to `Label` (default `WIN_LAG_<field>`): a verbatim copy, so a `u64` id renders its exact word. When `i - offset < 0` within the partition emits `default` if set, else `null`.

<!-- generated: contract -->

## Gotchas

- `order_by` required, `frame` forbidden, `offset < 0` refused — `PULSE_WINDOW_INVALID` on predict AND runtime.
- Partitioning by the raw `date` collapses each row to its own partition. Pick a coarser partition (region, account) and order by date.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-lead`, `op-win-pct-change`
