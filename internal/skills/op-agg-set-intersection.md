---
name: op-agg-set-intersection
description: Bitwise-AND a set field across rows; returns labels for every bit set in every contributing row.
kind: operator
category: AGG
operator: AGG_SET_INTERSECTION
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cohort-analysis]
---

<!-- generated: use-when -->

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Rich `[]string` — resolved dictionary labels. Scalar fallback = popcount of the intersection mask.

## Components

- `mask_intersection` — Bitwise AND across contributing rows; 4 little-endian words, `words[0]` = bits 0-63
- `popcount` — Bits set in `mask_intersection`
- `labels` — Resolved dictionary labels

- Margin reducibility: NOT margin-reducible — crosstab margins recompute from raw rows

<!-- generated: contract -->

## Gotchas

- AND across all rows ≠ AND across cells; do NOT pool margins.
- One row with all-zero bits → empty intersection for the group.
- Non-scalar row value: an overlay scoring it (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]`) is refused (`PROCESSING_CONFIG`, reason `non_scalar`), as is a chain stage carrying it that feeds another; an unscored one beside a scalar runs.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
