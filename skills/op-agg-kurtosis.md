---
name: op-agg-kurtosis
description: Bias-corrected excess kurtosis via online moments.
kind: operator
category: AGG
operator: AGG_KURTOSIS
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

Scalar `float64` — bias-corrected excess kurtosis. Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Running mean |
| `m2` | float64 | Second-moment accumulator |
| `m3` | float64 | Third-moment accumulator |
| `m4` | float64 | Fourth-moment accumulator |
| `kurtosis` | float64 | Derived from m2, m4, n |

- Mergeability: `Mergeable` (Chan moments combine)
- Streaming: per-chunk online moments

## Gotchas

- Requires `n >= 4` for bias correction; below → NaN.
- Excess kurtosis (normal = 0), NOT raw kurtosis (normal = 3).
- `decimal128` rejected.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
