---
name: op-agg-max
description: Largest non-null value of the field.
kind: operator
category: AGG
operator: AGG_MAX
type: reference
applies_to: process, compose, predict
examples_tags: [financial, streaming-friendly]
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
| `max` | float64 | Largest non-null value observed |

- Mergeability: `Mergeable`
- Streaming: per-chunk emits running max

## Gotchas

- Null rows skipped; `n_null` counts them.
- All-null cohort → emits NaN.
- Pair with `AGG_MIN` for a manual spread, or use `AGG_RANGE` for the diff.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
