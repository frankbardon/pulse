---
name: op-agg-set-union
description: Bitwise-OR a set field across rows; returns labels for every bit set in any contributing row.
kind: operator
category: AGG
operator: AGG_SET_UNION
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

Rich `[]string` — resolved dictionary labels. Scalar fallback = popcount of the union mask.

## Components

- `mask_union` — Bitwise OR across contributing rows; 4 little-endian words, `words[0]` = bits 0-63
- `popcount` — Bits set in `mask_union`
- `labels` — Resolved dictionary labels

<!-- generated: contract -->

## Gotchas

- Empty mask is a valid "no selection" — distinct from null.
- Union mask only honors bits seen in non-null rows.
- Non-scalar row value: an overlay scoring it (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]`) is refused (`PROCESSING_CONFIG`, reason `non_scalar`), as is a chain stage carrying it that feeds another; an unscored one beside a scalar runs.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
