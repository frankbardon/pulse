---
name: op-agg-range
description: Spread (max minus min) of the field across the input set.
kind: operator
category: AGG
operator: AGG_RANGE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
---

<!-- generated: use-when -->

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — `max - min`. Per-group when wired under a grouper.

## Components

- `min` — Smallest non-null value
- `max` — Largest non-null value

<!-- generated: contract -->

## Gotchas

- Outlier-sensitive — one extreme blows the spread.
- No non-null value in the bucket (group / cell / cohort) → `null` (NaN), components too.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `aggregation-design`, `response-components`
