---
name: op-agg-average
description: Arithmetic mean of a numeric field across the input set.
kind: operator
category: AGG
operator: AGG_AVERAGE
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, data-quality]
---

<!-- generated: use-when -->

## Params

Weighted: Σw·x / Σw.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

- `sum` — Running sum; mean = sum / n.
- Weighted adds `sum_weighted`, `weighted_mean`, `m2_weighted`, `sum_weights_sq`, `weighted_variance` (m2/(Σw−1); `null` if Σw ≤ 1).

<!-- generated: contract -->

## Gotchas

- Null inputs skipped — mean is over `n` non-null, not the cohort.
- Binned sum over exact `n`: the same bits on every worker count and shard split; weighted `m2_weighted` / `weighted_variance` are Welford — within ULP, not bitwise.
- No non-null value in the bucket (or Σw = 0) → `null` (NaN), never 0.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
