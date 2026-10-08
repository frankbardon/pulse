```yaml
name: op-win-delta
description: Point difference against the row `periods` positions earlier in the ordered partition — the subtraction counterpart of a percent change.
kind: operator
category: WIN
operator: WIN_DELTA
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
```

Row-level output; no `Response.Components`.

## Use when

Adds the change from a set number of rows earlier in the field's own units, such as orders this month minus last month.

Questions it answers:

- By how many orders did each month go up or down on the month before?
- How many points did satisfaction move between waves?

Use something else:

- `WIN_PCT_CHANGE` when you want the change relative to the earlier value.

## Params

`periods` (int, default `1`) — lookback distance (≥ 1). Slots: `field` (required, numeric), `order_by` (≥1, numeric / `date`), `partition_by`, `frame` (forbidden).

## Inputs

`Field`: numeric — `u4`…`u64`, `f32`/`f64`, `date`. Not `decimal128`, `packed_bool`, categorical.

## Output

One `float64` per row on `Label` (default `WIN_DELTA_<field>`). `cur - prev` at `periods` back — the field's OWN units (points), never a ratio. Rows `i < periods` are `null`.

## Reading the output

- `value`: The change since the row periods rows earlier, in the field's own units: current - earlier. A delta of 12 on an order count is 12 more orders than the earlier row.
  - Sign: positive means the current value is above the earlier one; negative means the current value is below the earlier one.
  - Caveat: It is in the field's units, so a change of 10 is large on a small field and trivial on a large one; read WIN_PCT_CHANGE for the change relative to the earlier value.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- **A zero prior is a REAL delta, not a null.** A percent change nulls it because the division is undefined; subtraction has no such case, so `prev == 0` emits `cur`.
- **Frame rejection happens at CONSTRUCTION**, not just in predict — the factory returns `PROCESSING_CONFIG` for a non-nil `frame`. The rest of the family declares it in predict only, so a framed request skipping predict is ignored there but REFUSED here.
- `periods <= 0` rejected (`PULSE_WINDOW_INVALID`); either side null → `null`; rows NOT reordered (`Request.Sort`); buffered.

## See

- Skills: [`window-design`](window-design.md), [`op-win-pct-change`](op-win-pct-change.md), [`op-win-lag`](op-win-lag.md)
