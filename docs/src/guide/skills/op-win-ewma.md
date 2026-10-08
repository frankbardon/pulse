```yaml
name: op-win-ewma
description: Exponentially weighted moving average; s_i = alpha*x_i + (1-alpha)*s_{i-1}.
kind: operator
category: WIN
operator: WIN_EWMA
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Adds a smoothed series: each new value counts for a fixed share w, the previous smoothed level for 1 - w, so older values fade.

Questions it answers:

- What is the underlying level of daily demand once day-to-day noise is damped?
- Is the smoothed defect rate drifting up over recent batches?

Use something else:

- `WIN_MOVING_AVG` when you want every row in a fixed window to count equally.

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

## Reading the output

- `value`: A smoothed level of the field: s = alpha * current + (1 - alpha) * previous s, seeded with the partition's first value present. A value k rows back carries weight alpha * (1 - alpha)^k, so alpha near 1 follows the latest value closely and alpha near 0 changes slowly.
  - Caveat: The seed enters at full weight, so the start of each partition leans on the first value: with alpha 0.1 it still carries about 35% of the weight ten rows later (0.9^10). Read the early rows with care.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- `alpha` REQUIRED — missing or out of `(0, 1]` → `PULSE_WINDOW_INVALID`.
- Rows preceding the first non-null emit `null` (no seed).
- Null values emit `null` but state survives through to the next non-null row.
- Result rows are NOT reordered — use `Request.Sort`.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-moving-avg`](op-win-moving-avg.md), [`op-win-running-avg`](op-win-running-avg.md)
