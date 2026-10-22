```yaml
name: op-filter-exclude
description: Drop records whose field value appears in the supplied Values list.
kind: operator
category: FILTER
operator: FILTER_EXCLUDE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Drops the rows whose field holds one of the listed values and keeps the rest, such as removing test accounts.

Questions it answers:

- What are the totals once internal test accounts are taken out?
- How do the results change without the store that was closed for refit?

Use something else:

- `FILTER_INCLUDE` when you want to keep only the listed values.
- `FILTER_NULL` when you want to drop the rows where the field is missing.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Block-list. Categorical fields resolve labels through the dictionary; numeric fields parse each entry via `strconv.ParseFloat`. |

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Drop when `Field`'s value is in `Values`; pass otherwise. No emitted column.

## Contract

- Types (`field`): `categorical_*`, `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n_in`, `n_out`, `n_null_input`.

## Gotchas

- Null rows PASS this filter (an include filter drops them).
- Unknown categorical label in `Values` → `PROCESSING_CONFIG` at build time, surfaced via predict.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-include`](op-filter-include.md)
