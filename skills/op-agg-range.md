---
name: op-agg-range
description: Spread (max minus min) of the field across the input set.
kind: operator
category: AGG
operator: AGG_RANGE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `packed_bool`, `nullable_*` |

## Output

Scalar `float64` — `max - min`. Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `min` | float64 | Smallest non-null value |
| `max` | float64 | Largest non-null value |

- Mergeability: `Mergeable`
- Streaming: per-chunk tracks (min, max)

## Gotchas

- Outlier-sensitive — one extreme blows the spread. Prefer IQR-style work via `AGG_PERCENTILE`.
- All-null cohort → NaN.
- For separate min and max use `AGG_MIN` / `AGG_MAX` directly.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
