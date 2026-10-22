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

| Name | Type | Default | Description |
|---|---|---|---|
| `periods` | int | `1` | Lookback distance (≥ 1). |

Slots: `field` (required, numeric), `order_by` (≥1, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `partition_by`, `frame` (forbidden).

## Inputs

`Field`: numeric; types in `## Contract`.

## Output

One `float64` per row on `Label` (default `WIN_DELTA_<field>`). `cur - prev` at `periods` back — the field's OWN units (points), never a ratio. Rows `i < periods` are `null`.

## Reading the output

- `value`: The change since the row periods rows earlier, in the field's own units: current - earlier. A delta of 12 on an order count is 12 more orders than the earlier row.
  - Sign: positive means the current value is above the earlier one; negative means the current value is below the earlier one.
  - Caveat: It is in the field's units, so a change of 10 is large on a small field and trivial on a large one; read WIN_PCT_CHANGE for the change relative to the earlier value.

## Contract

- Types (`field`): `date`, `f32`, `f64`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only.

## Gotchas

- **A zero prior is a REAL delta, not a null.** A percent change nulls it because the division is undefined; subtraction has no such case, so `prev == 0` emits `cur`.
- A `frame` is refused `PULSE_WINDOW_INVALID` by predict AND at construction, like every frame-less window.
- `periods <= 0` rejected (`PULSE_WINDOW_INVALID`); either side null → `null`; rows NOT reordered (`Request.Sort`); buffered.

## See

- Skills: [`window-design`](window-design.md), [`op-win-pct-change`](op-win-pct-change.md), [`op-win-lag`](op-win-lag.md)
