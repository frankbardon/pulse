---
name: op-filter-set-equals
description: Keep records whose set field mask exactly equals the supplied label mask.
kind: operator
category: FILTER
operator: FILTER_SET_EQUALS
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single equality. |

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Pass when `row == query`. No emitted column.

<!-- generated: contract -->

## Gotchas

- Empty `Values` resolves to query=0 — only rows whose set is exactly empty pass. An empty mask is a valid "no selection" set value, distinct from null.
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Null rows DROP.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-set-contains-any`, `op-filter-set-contains-all`, `op-filter-set-contains-none`
