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

<!-- generated: use-when -->

## Params

Weighted: the variance is the sample m2_w / (Σw − 1); `N` stays the contributing row count.

## Inputs

- `Field` — strict scalar numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`

## Output

Scalar `float64` — running mean (NaN no rows; `null` in JSON). Rich: `WelfordTriple{Mean, Variance, N}` via `RichAggregator`.

<!-- generated: reading-the-output -->

## Components

- `mean` — Welford running mean
- `m2` — Sum of squared deviations
- `variance` — Unbiased sample variance (`m2/(n-1)`); `null` when `n < 2` (weighted Σw ≤ 1)
- `stddev` — Sample standard deviation

<!-- generated: contract -->

## Gotchas

- Sample variance (`n-1`), not population (`n`); undefined (`null`, triple and components) below 2 values (Σw ≤ 1), never 0 — a pairwise / t / z overlay skips that leg.
- The rich triple is what cell-level t / z overlays read, via `Components.Crosstab.CellComponents`.
- Margin reducibility = recompute, not pool by addition.
- Non-scalar row value: an overlay scoring it (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]`) is refused (`PROCESSING_CONFIG`, reason `non_scalar`) — except the two-sample t / z vs-ref and vs-prior overlays, which read the triple from the slot's per-group components (disabled → `PULSE_OVERLAY_COMPONENTS_REQUIRED`)<!-- feature: OVERLAY_PROP_Z_VS_PRIOR -->; `OVERLAY_PROP_Z_VS_PRIOR` refuses it with its own `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`measure_type`)<!-- /feature -->; an unscored one beside a scalar runs.

## See

- `pulse_examples_search tags=[welford-triple]`
- Skills: `aggregation-design`, `overlay-system`, `response-components`
