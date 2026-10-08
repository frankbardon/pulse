```yaml
name: op-win-pct-change
description: Fractional change (0.05 = 5%) against the row `periods` positions earlier in the ordered partition.
kind: operator
category: WIN
operator: WIN_PCT_CHANGE
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Adds the change from a set number of rows earlier as a fraction of the earlier value: 0.05 is a 5% rise on last month.

Questions it answers:

- By what percentage did each month's revenue grow on the month before?
- Which regions grew fastest relative to their own previous quarter?

Use something else:

- `WIN_DELTA` when the earlier value can be 0, near 0 or negative, or the field is itself a percentage.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `periods` | int | `1` | Lookback distance (≥ 1) for the comparison row. |

`partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (forbidden). `field` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no `packed_bool`, no categorical) |

## Output

One `float64` per row written to `Label` (default `WIN_PCT_CHANGE_<field>`). `(cur - prev) / prev`, `prev` being `periods` rows back: a FRACTION (0.05 = 5%), never ×100. Rows `i < periods` within the partition emit `null`.

## Reading the output

- `value`: The change since the row periods rows earlier, as a fraction of that earlier value: (current - earlier) / earlier. 0.05 is a 5% rise, -0.2 a 20% fall and 1 a doubling; the value is not multiplied by 100.
  - Sign: positive means the current value is above the earlier one (when the earlier value is positive); negative means the current value is below the earlier one (when the earlier value is positive).
  - Caveat: On a field that can be negative the sign follows the division, not the direction: a rise from -10 to -5 reads -0.5. Use WIN_DELTA for such a field.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- `periods <= 0` REJECTED at predict (`PULSE_WINDOW_INVALID`).
- `prev == 0` emits `null` (no `+Inf`); a negative `prev` flips the sign.
- Either side null emits `null`.
- Result rows are NOT reordered — use `Request.Sort`.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-lag`](op-win-lag.md), [`overlay-system`](overlay-system.md)
