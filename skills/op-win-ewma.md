---
name: op-win-ewma
description: Exponentially weighted moving average; s_i = alpha*x_i + (1-alpha)*s_{i-1}.
kind: operator
category: WIN
operator: WIN_EWMA
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Window operators emit row-level values; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `alpha` | float | (required) | Smoothing factor in `(0, 1]`; higher = more weight on recent. |

`partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (REQUIRED `{mode: "rows"}`; ignored by the recurrence). `field` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, categorical, `packed_bool`) |

## Output

One `float64` per row written to `Label` (default `WIN_EWMA_<field>`). Recurrence seeds from the first non-null value in the partition.

## Gotchas

- `alpha` REQUIRED — missing or out of `(0, 1]` → `PULSE_WINDOW_INVALID`.
- Rows preceding the first non-null emit `null` (no seed).
- Null values emit `null` but state survives through to the next non-null row.
- Result rows are NOT reordered — use `Request.Sort`.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `window-design`, `op-win-moving-avg`, `op-win-running-avg`
