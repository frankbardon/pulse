---
name: op-win-delta
description: Point difference against the row `periods` positions earlier in the ordered partition — the subtraction counterpart of WIN_PCT_CHANGE.
kind: operator
category: WIN
operator: WIN_DELTA
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
---

Row-level output; no `Response.Components`.

## Params

`periods` (int, default `1`) — lookback distance (≥ 1). Slots: `field` (required, numeric), `order_by` (≥1, numeric / `date`), `partition_by`, `frame` (forbidden).

## Inputs

`Field`: numeric — `u4`…`u64`, `f32`/`f64`, `date`. Not `decimal128`, `packed_bool`, categorical.

## Output

One `float64` per row on `Label` (default `WIN_DELTA_<field>`). `cur - prev` at `periods` back — the field's OWN units (points), never a ratio. Rows `i < periods` are `null`.

## Gotchas

- **A zero prior is a REAL delta, not a null.** The one divergence from `WIN_PCT_CHANGE`, which nulls it because the division is undefined; subtraction has no such case, so `prev == 0` emits `cur`.
- **Frame rejection happens at CONSTRUCTION**, not just in predict — the factory returns `PROCESSING_CONFIG` for a non-nil `frame`. The rest of the family declares it in predict only, so a framed request skipping predict is ignored there but REFUSED here.
- `periods <= 0` rejected (`PULSE_WINDOW_INVALID`); either side null → `null`; rows NOT reordered (`Request.Sort`); buffered.

## See

- Skills: `window-design`, `op-win-pct-change`, `op-win-lag`
