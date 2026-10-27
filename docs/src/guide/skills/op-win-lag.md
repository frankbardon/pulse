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
| `default` | any | — | Substitute when offset crosses partition start; absent: `null`. |

`partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (forbidden).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One value per row written to `Label` (default `WIN_LAG_<field>`): a verbatim copy, so a `u64` id renders its exact word. When `i - offset < 0` within the partition emits `default` if set, else `null`.

## Contract

- Types (`field`): `date`, `f32`, `f64`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only.

## Gotchas

- `order_by` required, `frame` forbidden, `offset < 0` refused — `PULSE_WINDOW_INVALID` on predict AND runtime.
- Partitioning by the raw `date` collapses each row to its own partition. Pick a coarser partition (region, account) and order by date.
- Regressions, post-window formulas, aggregations and groupers may read the label ([`regression-model-options`](regression-model-options.md), [`windows-first`](windows-first.md)).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-lead`](op-win-lead.md), [`op-win-pct-change`](op-win-pct-change.md)
