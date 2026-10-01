---
name: op-group-range
description: Partition numeric records into half-open ranges [a, b); Interval controls bucket width.
kind: operator
category: GROUP
operator: GROUP_RANGE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `interval` | float | (required) | Bucket width on value axis; set on `Group.Interval`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64` (no `decimal128`) |

## Output

String label per row (e.g. `"[10, 20)"`). Smart default (Interval=10) for numeric.

## Components

Universal floor `{total_n, n_null}` plus:

| Key | Type | Notes |
|---|---|---|
| `interval` | float64 | Bucket width |
| `range_min` | float64 | Smallest non-null |
| `range_max` | float64 | Largest non-null |
| `n_buckets` | int | Buckets emitted |
| `edges` | []float64 | Sorted cutpoints |
| `buckets` | []bucket | `{key, low, high, count}` |
| `underflow_count` | int | Below low |
| `overflow_count` | int | At/above high |

- Mergeability: `Mergeable`
- Streaming: `StreamableGrouper` — eligible for fused crosstab

## Gotchas

- Half-open `[low, high)` — upper edge belongs to next bucket.
- Rejects categorical/decimal128 at construction.
- `Group.Include` not honoured.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `op-group-rounded`
