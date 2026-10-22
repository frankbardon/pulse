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

None operator-level. `partition_by` (carve), `order_by` (≥1, required, any type but `set_*` (`PULSE_WINDOW_INVALID`) or `vec_*` (`PULSE_VECTOR_FIELD_NOT_SCALAR`)), `frame` (REQUIRED — mode `"rows"`; typical `{preceding: null, following: 0}` for cumulative-to-current-row). `field` (required, numeric).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

One `float64` per row written to `Label` (default `WIN_RUNNING_SUM_<field>`). Sum of non-null values inside the resolved frame within the partition. Empty slice → `null`.

## Contract

- Types (`field`): `date`, `f32`, `f64`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only.

## Gotchas

- Frame REQUIRED (`PULSE_WINDOW_INVALID` without one, predict and runtime) — `null` preceding = UNBOUNDED PRECEDING.
- Bounded frames produce a windowed sum, not cumulative.
- Nulls skipped (not zero-filled).
- Result rows are NOT reordered — use `Request.Sort`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`window-design`](window-design.md), [`op-win-running-avg`](op-win-running-avg.md), [`op-win-moving-avg`](op-win-moving-avg.md)
