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

Weight-aware (`"weight": null` opts out): expanded-index type 7 — sort by value; x(k) = first value with cumulative Σw > k; h = 0.5·(Σw−1); interpolate x(⌊h⌋)..x(⌈h⌉). `probability` weights are first rescaled to Σw = n (rows used) — scale-invariant; `frequency` weights stay raw = duplicated rows. Invalid weights excluded (`PULSE_WEIGHT_INVALID_ROWS`).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `position_low` | int | Lower bracket index (weighted: ⌊h⌋, expanded) |
| `position_high` | int | Upper bracket index (weighted: ⌈h⌉) |
| `median` | float64 | Resolved median (linear interpolation) |

- Mergeability: `None` — exact median needs full sort
- Streaming: NOT streamable (buffered only).

## Gotchas

- Buffered-only path: full input materialised before sort.
- Outlier-robust, unlike the mean.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
