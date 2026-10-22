---
name: op-agg-min
description: Smallest non-null value of the field.
kind: operator
category: AGG
operator: AGG_MIN
type: reference
applies_to: process, compose, predict
examples_tags: [financial, streaming-friendly]
---

<!-- generated: use-when -->

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

- `min` — Smallest non-null value observed

<!-- generated: contract -->

## Gotchas

- Null rows skipped; `n_null` counts them.
- No non-null value in the bucket (group / cell / cohort) → `null` (NaN), components too.
- Decimal128 preserved on input; float64 output coerces the comparison space.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
