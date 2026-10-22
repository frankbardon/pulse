```yaml
name: op-win-share
description: Each row's share of its partition's total (x / Σx), or with cumulative the running share down the ordered rows — a capture curve.
kind: operator
category: WIN
operator: WIN_SHARE
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, window-operator, buffered-pipeline]
```

Row-level output; no `Response.Components`.

## Use when

Adds each row's share of its partition's total, or the running share down the ordered rows, such as the share of defaults caught so far.

Questions it answers:

- What share of all defaults falls in each score decile, and how much do the riskiest deciles capture together?
- What fraction of each region's revenue does each store bring in?

Use something else:

- `WIN_RUNNING_SUM` when you want the running total itself, not as a share.

## Params

`cumulative` (bool, default `false`). Slots: `field` (required), `partition_by`, `order_by` (only with `cumulative: true`, then required), `frame` (refused).

## Inputs

`Field`: numeric (types in `## Contract`). Usually an aggregation label on grouped rows.

## Output

One `float64` per row on `Label` (default `WIN_SHARE_<field>`), a fraction: `x / Σx` over the partition. Cumulative: running Σx (unbounded preceding → current row, `order_by` order) / Σx, exactly `1` on the last row.

## Reading the output

- `value`: The row's value as a fraction of its partition's total: 0.25 is a quarter, not 25. With cumulative it is the running total up to this row over the same total, rising to exactly 1 on the partition's last row with a value.
  - Caveat: The total is the partition's, over the values present: a missing value adds nothing, so shares of a column with gaps sum to 1 over the rows that answered.
  - Caveat: A cumulative share depends on the order: ordered by a score from riskiest down it is the capture curve; set desc for that.

## Contract

- Types (`field`): `date`, `f32`, `f64`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form and no slot weight; a request weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only.

## Gotchas

- Nulls add nothing; a null row reads `null` (cumulative: carries the running share). A zero total reads `null`, never 0.
- Refusals are `PULSE_WINDOW_INVALID` on predict AND runtime: `frame` set, `cumulative` without `order_by`, missing `field`.

## See

- `pulse_examples_search tags=[proportion-analysis, window-operator]`
- Skills: [`window-design`](window-design.md), [`op-win-lift`](op-win-lift.md), [`op-win-running-sum`](op-win-running-sum.md)
