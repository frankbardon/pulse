```yaml
name: op-filter-set-contains-all
description: Keep records whose set field has every bit in the supplied label mask set.
kind: operator
category: FILTER
operator: FILTER_SET_CONTAINS_ALL
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Keeps the rows whose multi-select answer includes every listed option, other options allowed, such as people who use both A and B.

Questions it answers:

- How do people who use both our app and our website rate us?
- Which tickets carry both the urgent and the billing tags?

Use something else:

- `FILTER_SET_CONTAINS_ANY` when one of the listed options is enough.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single bitwise AND-eq. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` |

## Output

Row-level predicate. Pass when `row & query == query`. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per [`response-components`](response-components.md) contract. Mergeable across chunks.

## Gotchas

- Empty `Values` resolves to query=0 — every row passes (trivially "contains all of nothing").
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Null rows DROP. Use this for AND-of-features survey logic ("respondents who picked X and Y").

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-set-contains-any`](op-filter-set-contains-any.md), [`op-filter-set-contains-none`](op-filter-set-contains-none.md), [`op-filter-set-equals`](op-filter-set-equals.md)
