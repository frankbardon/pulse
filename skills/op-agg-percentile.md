---
name: op-agg-percentile
description: Configurable percentile of the field; requires sorting the full value set.
kind: operator
category: AGG
operator: AGG_PERCENTILE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `percentile` | float | (required) | Percentile in `[0, 100]`. e.g. 95 for p95. |

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
| `p` | float64 | Requested percentile |
| `position` | int | Index in sorted set |
| `lower` | float64 | Lower bracket value |
| `upper` | float64 | Upper bracket value |
| `method` | string | Interpolation method (e.g. `"linear"`) |
| `value` | float64 | Resolved percentile |

- Mergeability: `None` — exact percentile needs full sort
- Streaming: NOT streamable

## Gotchas

- Buffered full-input path; cohort-sized memory peak.
- Out-of-range `percentile` rejected.
- For p50 prefer `AGG_MEDIAN` (semantically identical).

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
