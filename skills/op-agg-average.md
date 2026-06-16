---
name: op-agg-average
description: Arithmetic mean of a numeric field across the input set.
kind: operator
category: AGG
operator: AGG_AVERAGE
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, data-quality]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `packed_bool`, `nullable_*` |

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `sum` | float64 | Running sum; mean = sum / n |

- Mergeability: `Mergeable`
- Streaming: per-chunk emits running mean via (sum, n)

## Gotchas

- Null inputs skipped — mean is over `n` non-null, not the cohort.
- For weighted mean use `AGG_WEIGHTED_MEAN`.
- For streaming central tendency under high-precision needs use `AGG_WELFORD` (returns rich triple).

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
