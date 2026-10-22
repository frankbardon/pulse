---
name: op-filter-set-contains-all
description: Keep records whose set field has every bit in the supplied label mask set.
kind: operator
category: FILTER
operator: FILTER_SET_CONTAINS_ALL
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single bitwise AND-eq. |

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Pass when `row & query == query`. No emitted column.

<!-- generated: contract -->

## Gotchas

- Empty `Values` resolves to query=0 — every row passes (trivially "contains all of nothing").
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Null rows DROP. Use this for AND-of-features survey logic ("respondents who picked X and Y").

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-set-contains-any`, `op-filter-set-contains-none`, `op-filter-set-equals`
