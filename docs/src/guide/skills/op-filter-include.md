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

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type EXCEPT `set_*` (categorical_*, numeric, date, datetime, packed_bool, decimal128) |

## Output

Row-level predicate. Pass when `Field`'s value is in `Values`; drop otherwise. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per [`response-components`](response-components.md) contract. Mergeable across chunks; counters fold by simple addition.

## Gotchas

- Null rows fail the predicate (dropped).
- Unknown categorical label in `Values` → `PROCESSING_CONFIG` at build time, surfaced via predict.
- Non-numeric values on a numeric field → `PROCESSING_CONFIG` (parse error).
- Filters chain in declared order; this one sees only rows the previous kept.
- `set_*` rejected at build time with `PROCESSING_CONFIG` — a bitmask has no comparable scalar at any rung.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md), [`op-filter-exclude`](op-filter-exclude.md)
