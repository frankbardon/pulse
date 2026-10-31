```yaml
name: op-filter-include
description: Keep records whose field value appears in the supplied Values list.
kind: operator
category: FILTER
operator: FILTER_INCLUDE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
```

## Use when

Keeps only the rows whose field holds one of the listed values, such as two regions or three product codes.

Questions it answers:

- What do the figures look like for the North and South regions only?
- How did customers on the two premium plans answer?

Use something else:

- `FILTER_EXCLUDE` when you want to drop the listed values and keep everything else.
- `FILTER_SET_CONTAINS_ANY` when the field is a multi-select answer.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Allow-list. Categorical fields resolve labels through the dictionary; numeric fields parse each entry via `strconv.ParseFloat`. |

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Pass when `Field`'s value is in `Values`; drop otherwise. No emitted column.

## Contract

- Types (`field`): `categorical_*`, `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n_in`, `n_out`, `n_null_input`.

## Gotchas

- Null rows fail the predicate (dropped).
- Unknown categorical label in `Values` → `PROCESSING_CONFIG` when the RUN builds the filter; predict does not check labels (`valid: true`), so read the dictionary (inspect) first.
- Non-numeric values on a numeric field → `PROCESSING_CONFIG` (parse error).
- Filters chain in declared order; this one sees only rows the previous kept.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-exclude`](op-filter-exclude.md)
