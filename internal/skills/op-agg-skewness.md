---
name: op-agg-skewness
description: Population skewness g1 (m3 / m2^1.5, dividing by n) via online moments.
kind: operator
category: AGG
operator: AGG_SKEWNESS
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Weighted: weighted population moments, Σw in place of n.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — population skewness g1 = `m3 / m2^1.5` (moments divide by `n`), NOT the adjusted G1 of Excel `SKEW` / SPSS. Per-group when wired under a grouper.

<!-- generated: reading-the-output -->

## Components

- `mean` — Running mean
- `m2` — Squared-deviation accumulator
- `m3` — Cubed-deviation accumulator
- `skewness` — Derived from m2, m3, n

<!-- generated: contract -->

## Gotchas

- `n = 0` (or Σw = 0), `n = 1` or zero variance → `null` (NaN), scalar and component: no shape is visible there, never 0.
- Small-n bias: G1 = g1·√(n(n−1))/(n−2), so |g1| < |G1| on small groups.
- `decimal128` rejected<!-- feature: ATTR_FORMULA --> — cast via `ATTR_FORMULA`<!-- /feature -->.
- Sensitive to outliers; pre-filter or use rank-based alternatives.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
