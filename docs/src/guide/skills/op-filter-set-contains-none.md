```yaml
name: op-filter-set-contains-none
description: Keep records whose set field shares no bits with the supplied label mask.
kind: operator
category: FILTER
operator: FILTER_SET_CONTAINS_NONE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Keeps the rows whose multi-select answer includes none of the listed options, such as people who use neither A nor B.

Questions it answers:

- What do people who use none of the competitor apps think?
- Which orders had no discount code applied?

Use something else:

- `FILTER_SET_CONTAINS_ANY` when you want rows that include one of the options.
- `FILTER_EXCLUDE` when the field holds one value per row, not several.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Dictionary labels. Resolved at build time to a single query mask; per-row check is a single bitwise AND-eq-zero. |

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Pass when `row & query == 0`. No emitted column.

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n_in`, `n_out`, `n_null_input`.

## Gotchas

- Null rows PASS (like an exclude filter); add a null filter if you need them out too.
- Unknown label in `Values` → `PROCESSING_CONFIG`.
- Label whose dictionary bit position exceeds the set's width → `PROCESSING_CONFIG`.
- Useful as a NOT-of-features survey filter ("respondents who picked neither X nor Y").

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-set-contains-any`](op-filter-set-contains-any.md), [`op-filter-set-contains-all`](op-filter-set-contains-all.md), [`op-filter-set-equals`](op-filter-set-equals.md)
