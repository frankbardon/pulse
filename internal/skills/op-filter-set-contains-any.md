---
name: op-filter-set-contains-any
description: Keep records whose set field shares at least one bit with the supplied label mask.
kind: operator
category: FILTER
operator: FILTER_SET_CONTAINS_ANY
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Wire: `values` is a top-level filterer key (beside `field`), not under `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | array | — | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single bitwise AND. |

## Inputs

`field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Pass when `row & query != 0`. No emitted column.

<!-- generated: contract -->

## Gotchas

- Empty `values` resolves to a zero query — every row drops. Validate caller input.
- Unknown label in `values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Null rows DROP (like an include filter).

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-set-contains-all`, `op-filter-set-contains-none`, `op-filter-set-equals`
