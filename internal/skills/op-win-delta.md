---
name: op-win-delta
description: Point difference against the row `periods` positions earlier in the ordered partition — the subtraction counterpart of a percent change.
kind: operator
category: WIN
operator: WIN_DELTA
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Row-level output; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `periods` | int | `1` | Lookback distance (≥ 1). |

Slots: `field` (required, numeric), `order_by` (≥1, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `partition_by`, `frame` (forbidden).

## Inputs

`Field`: numeric; types in `## Contract`.

## Output

One `float64` per row on `Label` (default `WIN_DELTA_<field>`). `cur - prev` at `periods` back — the field's OWN units (points), never a ratio. Rows `i < periods` are `null`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- **A zero prior is a REAL delta, not a null.** A percent change nulls it because the division is undefined; subtraction has no such case, so `prev == 0` emits `cur`.
- A `frame` is refused `PULSE_WINDOW_INVALID` by predict AND at construction, like every frame-less window.
- `periods <= 0` rejected (`PULSE_WINDOW_INVALID`); either side null → `null`; rows NOT reordered (`Request.Sort`); buffered.

## See

- Skills: `window-design`, `op-win-pct-change`, `op-win-lag`
