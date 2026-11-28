---
name: op-group-range
description: Partition numeric records into half-open ranges [a, b); `interval` sets the bucket width.
kind: operator
category: GROUP
operator: GROUP_RANGE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `interval` | float | `1` | Bucket width; the slot's top-level `interval`, not `params`. Absent or `0` (unset) = 1; negative → `PROCESSING_CONFIG`, at predict and at run time. |

## Inputs

`field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

String label `"low-high"` per row (`"10-20"` holds `[10, 20)`). Rows emit by numeric low edge (`"-20--10"`, `"-10-0"`, `"0-10"`, `"10-20"`, `"100-110"`) on every arm (streaming, shards, fused crosstab). An explicit `sort` or crosstab-axis `include` wins; sorting the key column compares strings.

## Components

- `interval` — Bucket width
- `range_min` — Smallest non-null
- `range_max` — Largest non-null
- `n_buckets` — Buckets emitted
- `edges` — Sorted cutpoints
- `buckets` — `{key, low, high, count}`
- `underflow_count` — Below low
- `overflow_count` — At/above high
- Fans out whatever the bucket count; at very high cardinality (buckets ≳ 70% of each partition's rows) the fan-out gains nothing, which predict cannot see.
- Eligible for the fused crosstab.

<!-- generated: contract -->

## Gotchas

- Half-open `[low, high)` — upper edge belongs to next bucket.
- Explicit `"type":"GROUP_RANGE"` without `interval` buckets at width 1; a numeric slot with no `type` gets the smart default GROUP_RANGE with `interval` 10 (`defaults_applied`).
- `include` on `groups` is refused (`PROCESSING_CONFIG`); a crosstab axis keeps it for order.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `grouper-design`, `op-group-rounded`
