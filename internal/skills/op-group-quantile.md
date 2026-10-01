---
name: op-group-quantile
description: Partition records into N equal-population quantile buckets (Q1..Q4 / D1..D10 / P1..P100).
kind: operator
category: GROUP
operator: GROUP_QUANTILE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `interval` | int | 4 | Number of buckets; set on `Group.Interval`. 4 (quartiles), 10 (deciles), 100 (percentiles). |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64` (no `decimal128`) |

## Output

Bucket label per row (`Qk` / `Dk` / `Pk`).

## Components

Universal floor `{total_n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `n_quantiles` | int | Equal-population buckets |
| `method` | string | Interpolation (`"linear"`) |
| `edges` | []float64 | Sorted cutpoints |
| `buckets` | []bucket | `{key, low, high, count}` |

- Mergeability: `None` — `BufferedComponents=true`; needs sorted full input
- Streaming: `Streamable=false` — terminal-only. Use `GROUP_RANGE`/`GROUP_ROUNDED` for streaming.

## Gotchas

- Forces buffered execution — disables fused crosstab.
- Rejects categorical/decimal128 at construction.
- `Group.Include` not honoured — buckets are derived ranks.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `grouper-design`, `response-components`, `op-group-range`
