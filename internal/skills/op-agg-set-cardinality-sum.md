---
name: op-agg-set-cardinality-sum
description: Sum of popcounts across contributing rows — total label selections seen.
kind: operator
category: AGG
operator: AGG_SET_CARDINALITY_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

None. Weight-aware (`"weight": null` opts out): Σw·popcount (a float).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `int64` — total selections seen across rows (weighted: `float64`).

## Components

- `sum_cardinality` — Sum of popcounts

<!-- generated: contract -->

## Gotchas

- Counts label selections, not rows — `sum_cardinality ≥ n` for any non-empty masks.
- Empty masks contribute 0 (and count toward `n`).
- Weighted Σw figures are binned sums: the same bits on every worker count and shard split.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
