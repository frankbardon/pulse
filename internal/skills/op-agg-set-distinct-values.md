---
name: op-agg-set-distinct-values
description: Count of distinct exact mask values seen; each combination is atomic.
kind: operator
category: AGG
operator: AGG_SET_DISTINCT_VALUES
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

Scalar `int64` — count of distinct exact masks.

## Components

- `mask_union` — Bitwise OR of every row's mask; 4 little-endian words, `words[0]` = bits 0-63
- `popcount` — Distinct labels observed
- `labels` — Resolved dictionary labels

<!-- generated: contract -->

## Gotchas

- Treats each unique combination as atomic — `{Visa, Amex}` ≠ `{Visa}`.
- Components carry the union, NOT a list of distinct masks (mask space can be huge).

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `cohort-schema-design`, `response-components`
