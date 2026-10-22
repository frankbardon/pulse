---
name: op-agg-mode
description: Most-frequent value of the field (ties broken by the smallest value).
kind: operator
category: AGG
operator: AGG_MODE
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cross-tabulation]
---

<!-- generated: use-when -->

## Params

Weighted: the value with the largest Σw; `count` becomes that Σw (a float).

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — the modal value (categorical: its dictionary index). Per-group under a grouper. ProcessChain admits it: a later stage reads that index as a number, not a label.

## Components

- `value` — Most-frequent value
- `count` — Row count of the modal value (weighted: Σw)
- `distinct_count` — Distinct values observed
- `tie_count` — Values tied at the max count

<!-- generated: contract -->

## Gotchas

- Ties go to the SMALLEST value (or index), not the first seen; `tie_count > 1` flags a tie.
- High cardinality costs memory; pre-filter.
- No non-null value → `null` (NaN), never 0.
- Over `categorical_*` / `packed_bool` the value is a CODE: an overlay scoring it (any host; `params.field`, else `aggregations[0]`) is refused `PROCESSING_CONFIG`, reason `categorical_code` (+ `field`, `field_type`). So is a crosstab whose CELL is a code, under any MATRIX kind that reads cell values (`slot: "crosstab.cell"`; the moment pairwise kinds read none). Score a count or share instead. Code-ness survives ProcessChain: a later stage's column written by a code-emitting aggregation is still a code (`field_type` its origin type), so `AGG_MODE` over it, scored, is refused too.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `response-components`
