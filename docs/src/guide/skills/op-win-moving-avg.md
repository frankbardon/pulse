```yaml
name: op-win-moving-avg
description: Moving average of Field over a bounded Frame; both ends must be set.
kind: operator
category: WIN
operator: WIN_MOVING_AVG
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Adds a moving average: the mean of a field over a fixed number of neighbouring rows, smoothing short-term swings in a series.

Questions it answers:

- What is the trailing seven-day average of daily orders?
- Is the trend in weekly sign-ups rising once the week-to-week noise is smoothed out?

Use something else:

- `WIN_RUNNING_AVG` when you want the average of everything up to this row.

## Params

None operator-level. `partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (REQUIRED — mode `"rows"`, `preceding` AND `following` BOTH bounded). `field` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no `packed_bool`, no categorical) |

## Output

One `float64` per row written to `Label` (default `WIN_MOVING_AVG_<field>`). Mean of non-null values inside `[i - preceding, i + following]` within the partition. Empty slice → `null`.

## Reading the output

- `value`: The mean of the field over the rows in the frame around this one: from preceding rows before it to following rows after it, within the partition and in order_by order. preceding 6, following 0 is a trailing average of 7 rows.
  - Caveat: Near the start and end of each partition the frame runs off the edge and the mean covers fewer rows, so the first values are noisier and not comparable with the rest.
  - Caveat: Missing values are skipped, so the mean is over the values present, not the frame width; a frame with no value reads null.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- Unbounded frame on either end is REJECTED at predict.
- Trailing 7-row window: `frame: {mode: "rows", preceding: 6, following: 0}`.
- Nulls skipped (not zero-filled); denominator is non-null count.
- Result rows are NOT reordered — use `Request.Sort`.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-running-avg`](op-win-running-avg.md), [`op-win-ewma`](op-win-ewma.md)
