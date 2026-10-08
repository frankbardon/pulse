```yaml
name: op-filter-set-equals
description: Keep records whose set field mask exactly equals the supplied label mask.
kind: operator
category: FILTER
operator: FILTER_SET_EQUALS
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Keeps the rows whose multi-select answer is exactly the listed options, no more and no fewer, such as people who use only A.

Questions it answers:

- How many customers use our app and nothing else?
- Which respondents picked exactly these two reasons?

Use something else:

- `FILTER_SET_CONTAINS_ALL` when other options are allowed alongside the listed ones.
- `GROUP_SET_VALUE` when you want to see every combination with its count.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single equality. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` |

## Output

Row-level predicate. Pass when `row == query`. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per [`response-components`](response-components.md) contract. Mergeable across chunks.

## Gotchas

- Empty `Values` resolves to query=0 — only rows whose set is exactly empty pass. An empty mask is a valid "no selection" set value, distinct from null.
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Null rows DROP.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-set-contains-any`](op-filter-set-contains-any.md), [`op-filter-set-contains-all`](op-filter-set-contains-all.md), [`op-filter-set-contains-none`](op-filter-set-contains-none.md)
