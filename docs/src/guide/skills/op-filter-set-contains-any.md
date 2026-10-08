```yaml
name: op-filter-set-contains-any
description: Keep records whose set field shares at least one bit with the supplied label mask.
kind: operator
category: FILTER
operator: FILTER_SET_CONTAINS_ANY
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Keeps the rows whose multi-select answer includes at least one of the listed options, such as anyone who uses brand A or B.

Questions it answers:

- What do people who use either of our two apps think of the service?
- Which customers paid by any card at least once?

Use something else:

- `FILTER_SET_CONTAINS_ALL` when the row must include every listed option.
- `FILTER_INCLUDE` when the field holds one value per row, not several.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single bitwise AND. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` |

## Output

Row-level predicate. Pass when `row & query != 0`. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per [`response-components`](response-components.md) contract. Mergeable across chunks.

## Gotchas

- Empty `Values` resolves to a zero query — every row drops. Validate caller input.
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Null rows DROP (like an include filter).

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-set-contains-all`](op-filter-set-contains-all.md), [`op-filter-set-contains-none`](op-filter-set-contains-none.md), [`op-filter-set-equals`](op-filter-set-equals.md)
