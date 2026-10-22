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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `percentile` | float | (required) | Percentile in `[0, 100]`. e.g. 95 for p95. |

Weighted: Hmisc `wtd.quantile` — sort by value; h = p·(Σw−1); x(k) = first value with cumulative Σw ≥ k+1 (snapped to an integer within 1e-9); interpolate x(⌊h⌋)..x(⌈h⌉). `probability` weights first rescaled to Σw = n (rows used) — scale-invariant; `frequency` weights stay raw = type 7 on duplicated rows.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper.

<!-- generated: reading-the-output -->

## Components

- `p` — Requested percentile
- `position` — Index in sorted set (weighted: ⌊h⌋, expanded)
- `lower` — Lower bracket value
- `upper` — Upper bracket value
- `method` — Interpolation method (e.g. `"linear"`)
- `value` — Resolved percentile

<!-- generated: contract -->

## Gotchas

- Buffered full-input path; cohort-sized memory peak.
- Out-of-range `percentile` rejected.
- No non-null value → `null` (NaN), never 0.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
