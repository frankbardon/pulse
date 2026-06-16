---
name: op-filter-set-contains-none
description: Keep records whose set field shares no bits with the supplied label mask.
kind: operator
category: FILTER
operator: FILTER_SET_CONTAINS_NONE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single bitwise AND-eq-zero. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`, `set_u16`, `set_u32`, `set_u64` |

## Output

Row-level predicate. Pass when `row & query == 0`. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. Mergeable across chunks.

## Gotchas

- Null rows PASS (consistent with `FILTER_EXCLUDE`). Pair with `FILTER_NULL` if you need null exclusion too.
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Useful as a NOT-of-features survey filter ("respondents who picked neither X nor Y").

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-set-contains-any`, `op-filter-set-contains-all`, `op-filter-set-equals`
