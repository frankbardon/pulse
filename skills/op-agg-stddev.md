---
name: op-agg-stddev
description: Population standard deviation via Welford's online algorithm.
kind: operator
category: AGG
operator: AGG_STDDEV
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

Scalar `float64` — population stddev (n-denominator). Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `m2` | float64 | Sum of squared deviations |
| `variance` | float64 | `m2 / n` |
| `stddev` | float64 | √variance |

- Mergeability: `Mergeable` (Chan parallel-merge)
- Streaming: per-chunk; merge via Chan combine

## Gotchas

- Population stddev (`n` denominator), not sample (`n-1`). For sample variance use `AGG_WELFORD`.
- `decimal128` rejected — promote via `ATTR_FORMULA` cast.
- Single-row group → 0.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
