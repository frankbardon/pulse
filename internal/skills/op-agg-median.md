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

<!-- generated: use-when -->

## Params

Weighted: Hmisc `wtd.quantile` — sort by value; h = 0.5·(Σw−1); x(k) = first value with cumulative Σw ≥ k+1 (snapped to an integer within 1e-9); interpolate x(⌊h⌋)..x(⌈h⌉). `probability` weights first rescaled to Σw = n (rows used) — scale-invariant; `frequency` weights stay raw = type 7 on duplicated rows.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

- `position_low` — Lower bracket index (weighted: ⌊h⌋, expanded)
- `position_high` — Upper bracket index (weighted: ⌈h⌉)
- `median` — Resolved median (linear interpolation)

<!-- generated: contract -->

## Gotchas

- Buffered-only path: full input materialised before sort.
- Outlier-robust, unlike the mean.
- No non-null value → `null` (NaN), never 0.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
