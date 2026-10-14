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

<!-- generated: use-when -->

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `u4`; a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |

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

- Outlier-sensitive — one extreme blows the spread.
- No non-null value in the bucket (group / cell / cohort) → `null` (NaN), components too.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
