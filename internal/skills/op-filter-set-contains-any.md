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

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single bitwise AND. |

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Pass when `row & query != 0`. No emitted column.

<!-- generated: contract -->

## Gotchas

- Empty `Values` resolves to a zero query — every row drops. Validate caller input.
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Null rows DROP (like an include filter).

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-set-contains-all`, `op-filter-set-contains-none`, `op-filter-set-equals`
