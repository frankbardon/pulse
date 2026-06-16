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

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single bitwise AND-eq. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`, `set_u16`, `set_u32`, `set_u64` |

## Output

Row-level predicate. Pass when `row & query == query`. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. Mergeable across chunks.

## Gotchas

- Empty `Values` resolves to query=0 — every row passes (trivially "contains all of nothing").
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Null rows DROP. Use this for AND-of-features survey logic ("respondents who picked X and Y").

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-set-contains-any`, `op-filter-set-contains-none`, `op-filter-set-equals`
