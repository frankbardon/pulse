---
name: op-agg-kurtosis
description: Population excess kurtosis g2 (m4 / m2^2 - 3, dividing by n) via online moments.
kind: operator
category: AGG
operator: AGG_KURTOSIS
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

Scalar `float64` — population excess kurtosis g2 = `m4 / m2^2 − 3` (moments divide by `n`), NOT the adjusted G2 of Excel `KURT` / SPSS. Per-group when wired under a grouper.

<!-- generated: reading-the-output -->

## Components

- `mean` — Running mean
- `m2` — Second-moment accumulator
- `m3` — Third-moment accumulator
- `m4` — Fourth-moment accumulator
- `kurtosis` — Derived from m2, m4, n

<!-- generated: contract -->

## Gotchas

- `n = 0` (or Σw = 0), `n = 1` or zero variance → `null` (NaN), scalar and component: no shape is visible there, never 0.
- Small-n bias vs adjusted G2 is large on small groups; g2 ≥ −2 always.
- Excess kurtosis (normal = 0), NOT raw kurtosis (normal = 3).

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
