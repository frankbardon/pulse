---
name: op-agg-set-cardinality-avg
description: Average popcount per contributing row — typical number of selections.
kind: operator
category: AGG
operator: AGG_SET_CARDINALITY_AVG
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

None. Weight-aware (`"weight": null` opts out): Σw·popcount / Σw.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — `sum_cardinality / n`.

## Components

- `sum_cardinality` — Sum of popcounts
- `avg_cardinality` — Avg per row

- Margin: mean-reducible — needs per-cell `n` to combine

<!-- generated: contract -->

## Gotchas

- "Typical selections per respondent" — survey-friendly summary.
- Empty masks count toward `n` (lowering the average); pre-filter if you want only non-empty rows.
- No non-null mask (or Σw = 0) → `null` (NaN), never 0.
- Weighted Σw figures are binned sums: the same bits on every worker count and shard split.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
