---
name: op-agg-distinct-sum
description: Sum a value field once per distinct key.
kind: operator
category: AGG
operator: AGG_DISTINCT_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

`distinct_by` (string, required) — field holding the distinct key. Absent or empty: `PROCESSING_CONFIG`, never defaulted to `Field`; unknown: `SERVICE_VALIDATION`.

## Inputs

`Field` — admitted types: see `## Contract`.

- `distinct_by` — any numeric-coded field, `categorical_*` included

## Output

Scalar `float64` — one value per key, summed.

## Components

- `sum` — Sum of the first value seen per key.
- `distinct_count` — Distinct keys that contributed.
- `MarginIndependent` margins: crosstabs fuse.

<!-- generated: contract -->

## Gotchas

- FIRST VALUE WINS on a key with conflicting values; a merge keeps the receiver's, so it holds across shards too.
- The sum is binned: the same bits on every worker count and shard split when each key carries one value (a conflicting key keeps whichever value its arm saw first).
- Null in EITHER half contributes nothing and registers NO key; a later real row counts.
- A plain sum of a per-respondent weight multiplies it by row count; this gives the weighted base.
- Memory grows per key.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `crosstab-guide`
