---
name: op-group-rounded
description: Round each numeric value to the nearest multiple of Interval and group by the rounded scalar.
kind: operator
category: GROUP
operator: GROUP_ROUNDED
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `interval` | float | (required) | Rounding increment; set on `Group.Interval`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64` (no `decimal128`) |

## Output

Rounded numeric key per row. Each value snaps to the nearest multiple of `Interval`; bucket key is the rounded scalar.

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `precision` | float64 | Rounding increment (Group.Interval) |
| `edges` | []float64 | Sorted rounded scalars separating buckets |
| `buckets` | []bucket | `{key, low, high, count}` per emission |

- Mergeability: `Mergeable`
- Streaming: `StreamableGrouper` — eligible for fused crosstab

## Gotchas

- Reach for `GROUP_RANGE` for half-open intervals — `GROUP_ROUNDED` snaps to scalars.
- Rejects categorical/decimal128 at construction.
- `Group.Include` not honoured — filter source field instead.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `response-components`, `op-group-range`
