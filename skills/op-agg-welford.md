---
name: op-agg-welford
description: Streaming Welford-Pébaÿ moment triple — running mean, sample variance (n-1), and observed count.
kind: operator
category: AGG
operator: AGG_WELFORD
type: reference
applies_to: process, compose, predict
examples_tags: [welford-triple, distribution-shape, buffered-pipeline]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | strict scalar numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `nullable_u8`/`nullable_u16` |

`decimal128`, `date`, and bit-packed types rejected.

## Output

Scalar `float64` — running mean (NaN no rows). Rich: `WelfordTriple{Mean, Variance, N}` via `RichAggregator`.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `mean` | float64 | Welford running mean |
| `m2` | float64 | Sum of squared deviations |
| `variance` | float64 | Unbiased sample variance (`m2/(n-1)`) |
| `stddev` | float64 | Sample standard deviation |

- Mergeability: `Mergeable` (Chan-Welford combine); margin recompute
- Streaming: per-chunk; rich payload exposed at terminal flush

## Gotchas

- Sample variance (`n-1`), not population — differs from `AGG_VARIANCE`.
- Rich triple is the source of truth for `OVERLAY_T_CELL` / `OVERLAY_Z_CELL` via `Components.Crosstab.CellComponents`.
- Margin reducibility = recompute, not pool by addition.

## See

- `pulse_examples_search tags=[welford-triple]`
- Skills: `aggregation-design`, `overlay-system`, `response-components`
