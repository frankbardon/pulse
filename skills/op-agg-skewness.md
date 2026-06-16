---
name: op-agg-skewness
description: Bias-corrected skewness via online moments.
kind: operator
category: AGG
operator: AGG_SKEWNESS
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `nullable_*` |

## Output

Scalar `float64` — bias-corrected skewness. Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Running mean |
| `m2` | float64 | Squared-deviation accumulator |
| `m3` | float64 | Cubed-deviation accumulator |
| `skewness` | float64 | Derived from m2, m3, n |

- Mergeability: `Mergeable` (Chan moments combine)
- Streaming: per-chunk online moments

## Gotchas

- Requires `n >= 3` for bias correction; below → NaN.
- `decimal128` rejected — cast via `ATTR_FORMULA`.
- Sensitive to outliers; pre-filter or use rank-based alternatives.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
