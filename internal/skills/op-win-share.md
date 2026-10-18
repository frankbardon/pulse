---
name: op-win-share
description: Each row's share of its partition's total (x / Σx), or with cumulative the running share down the ordered rows — a capture curve.
kind: operator
category: WIN
operator: WIN_SHARE
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, window-operator, buffered-pipeline]
---

Row-level output; no `Response.Components`.

<!-- generated: use-when -->

## Params

`cumulative` (bool, default `false`). Slots: `field` (required), `partition_by`, `order_by` (only with `cumulative: true`, then required), `frame` (refused).

## Inputs

`Field`: numeric — `u4`…`u64`, `f32`/`f64`, `date`; any other type is `PULSE_FIELD_TYPE_UNSUPPORTED`. Usually an aggregation label on grouped rows.

## Output

One `float64` per row on `Label` (default `WIN_SHARE_<field>`), a fraction: `x / Σx` over the partition. Cumulative: running Σx (unbounded preceding → current row, `order_by` order) / Σx, exactly `1` on the last row.

<!-- generated: reading-the-output -->

## Gotchas

- Nulls add nothing; a null row reads `null` (cumulative: carries the running share). A zero total reads `null`, never 0.
- Refusals are `PULSE_WINDOW_INVALID` on predict AND runtime: `frame` set, `cumulative` without `order_by`, missing `field`.
- Not weightable: a request `weight` is `PROCESSING_CONFIG`. Buffered (`Streamable=false`); rows not reordered.

## See

- `pulse_examples_search tags=[proportion-analysis, window-operator]`
- Skills: `window-design`, `op-win-lift`, `op-win-running-sum`
