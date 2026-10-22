---
name: op-agg-distinct-count
description: Count distinct non-null values across the input set.
kind: operator
category: AGG
operator: AGG_DISTINCT_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `int64` — number of distinct non-null values. Per-group when wired under a grouper.

## Components

- `cardinality` — Distinct non-null values observed

<!-- generated: contract -->

## Gotchas

- High-cardinality fields → memory growth proportional to distinct values.
- Counts non-null only; nulls collapsed.
- Over `decimal128` the count is a JSON integer (`3`), never a decimal string; distinct by value at the column scale.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `response-components`
