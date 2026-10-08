```yaml
name: op-win-running-sum
description: Running total of Field over the configured Frame within the ordered partition.
kind: operator
category: WIN
operator: WIN_RUNNING_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Adds a running total of a field down the ordered rows, such as revenue for the year to date.

Questions it answers:

- What is the year-to-date revenue at the end of each month?
- How many responses had come in by each day of fieldwork?

Use something else:

- `AGG_SUM` when you want one total per group, not a running one.
- `WIN_RUNNING_AVG` when you want the running average instead of the total.

## Params

None operator-level. `partition_by` (carve), `order_by` (≥1, required, numeric / `date`), `frame` (REQUIRED — mode `"rows"`; typical `{preceding: null, following: 0}` for cumulative-to-current-row). `field` (required, numeric).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date` (no `decimal128`, no `packed_bool`, no categorical) |

## Output

One `float64` per row written to `Label` (default `WIN_RUNNING_SUM_<field>`). Sum of non-null values inside the resolved frame within the partition. Empty slice → `null`.

## Gotchas

- Not weightable: a request `weight` is `PROCESSING_CONFIG` (windows carry no slot weight); `Options.DefaultWeight` is skipped.
- Frame REQUIRED — `null` preceding = UNBOUNDED PRECEDING (full cumulative).
- Bounded frames produce a windowed sum, not cumulative.
- Nulls skipped (not zero-filled).
- Result rows are NOT reordered — use `Request.Sort`.
- Forces buffered execution (`Streamable=false`).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-running-avg`](op-win-running-avg.md), [`op-win-moving-avg`](op-win-moving-avg.md)
