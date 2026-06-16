---
name: op-agg-variance
description: Population variance via Welford's online algorithm.
kind: operator
category: AGG
operator: AGG_VARIANCE
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

Scalar `float64` — population variance (n-denominator). Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `m2` | float64 | Sum of squared deviations |
| `variance` | float64 | `m2 / n` |

- Mergeability: `Mergeable` (Chan parallel-merge)
- Streaming: per-chunk Welford

## Gotchas

- Population variance (`n`), not sample (`n-1`). For sample variance use `AGG_WELFORD`.
- `decimal128` rejected — cast via `ATTR_FORMULA`.
- Single-row group → 0.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
