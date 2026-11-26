```yaml
name: op-win-lead
description: Per-row value of the field from N rows later in the ordered partition.
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
| `default` | any | — | Substitute when offset crosses partition end; absent: `null`. Over a categorical: a dictionary label, else `PULSE_WINDOW_INVALID` `default_not_a_label`. |

`partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (forbidden).

## Inputs

`field` — admitted types: see `## Contract`.

## Output

One value per row written to `label` (default `WIN_LEAD_<field>`): a verbatim copy, so a `u64` id renders its exact word and a categorical its label — a lagged category on the source dictionary. When `i + offset >= partition_len` emits `default` if set, else `null`.

## Contract

- Types (`field`): `categorical_*`, `date`, `f32`, `f64`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only.

## Gotchas

- Mirror of a lag — same partition / order / frame rules (a lagged category included: [`op-win-lag`](op-win-lag.md)), opposite scan direction.
- `order_by` required, `frame` forbidden.
- Result rows are NOT reordered by `order_by` — use `sort` for response order.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-lag`](op-win-lag.md), [`op-win-pct-change`](op-win-pct-change.md)
