---
name: op-agg-min
description: Smallest non-null value of the field.
kind: operator
category: AGG
operator: AGG_MIN
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
| `min` | float64 | Smallest non-null value observed |

- Mergeability: `Mergeable`
- Streaming: per-chunk emits running min

## Gotchas

- Null rows skipped; `n_null` counts them.
- All-null cohort → emits NaN (sentinel for "no data").
- Decimal128 preserved on input; float64 output coerces the comparison space.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
