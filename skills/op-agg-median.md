---
name: op-agg-median
description: 50th percentile of the field; requires sorting the full value set.
kind: operator
category: AGG
operator: AGG_MEDIAN
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
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
| `position_low` | int | Lower bracket index in sorted set |
| `position_high` | int | Upper bracket index |
| `median` | float64 | Resolved median (linear interpolation) |

- Mergeability: `None` — exact median needs full sort
- Streaming: NOT streamable — use `AGG_AVERAGE` for a streaming proxy

## Gotchas

- Buffered-only path: full input materialised before sort.
- Outlier-robust unlike `AGG_AVERAGE`.
- For arbitrary percentiles use `AGG_PERCENTILE`.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
