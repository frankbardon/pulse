---
name: op-agg-zscore
description: Standardized z-score aggregate — mean-centered, stddev-scaled summary.
kind: operator
category: AGG
operator: AGG_ZSCORE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, comparison]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `nullable_*` |

## Output

Scalar `float64` — `(target - pop_mean) / pop_stddev`. Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `pop_mean` | float64 | Population mean (center) |
| `pop_stddev` | float64 | Population stddev (scale) |
| `target_value` | float64 | Value standardized |
| `zscore` | float64 | Resolved score |

- Mergeability: `Mergeable`
- Streaming: NOT streamable — finalize needs full deviation sum

## Gotchas

- Buffered path only; use `ATTR_ZSCORE` for per-row z-scores instead.
- Zero stddev → NaN.
- `decimal128` rejected.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `attribute-composition`, `response-components`
