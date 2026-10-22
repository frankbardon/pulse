---
name: op-win-pct-change
description: Fractional change (0.05 = 5%) against the row `periods` positions earlier in the ordered partition.
kind: operator
category: WIN
operator: WIN_PCT_CHANGE
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `periods` | int | `1` | Lookback distance (≥ 1) for the comparison row. |

`partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (forbidden). `field` (required, numeric).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One `float64` per row written to `Label` (default `WIN_PCT_CHANGE_<field>`). `(cur - prev) / prev`, `prev` being `periods` rows back: a FRACTION (0.05 = 5%), never ×100. Rows `i < periods` within the partition emit `null`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- `periods <= 0` REJECTED (`PULSE_WINDOW_INVALID`, predict and runtime).
- `prev == 0` emits `null` (no `+Inf`); a negative `prev` flips the sign.
- Either side null emits `null`.
- Result rows are NOT reordered — use `Request.Sort`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-lag`, `overlay-system`
