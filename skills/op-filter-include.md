---
name: op-filter-include
description: Keep records whose field value appears in the supplied Values list.
kind: operator
category: FILTER
operator: FILTER_INCLUDE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Allow-list. Categorical fields resolve labels through the dictionary; numeric fields parse each entry via `strconv.ParseFloat`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type (numeric, categorical_*, date, packed_bool, decimal128) |

## Output

Row-level predicate. Pass when `Field`'s value is in `Values`; drop otherwise. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. Mergeable across chunks; counters fold by simple addition.

## Gotchas

- Null rows fail the predicate (dropped). For null-aware logic use `FILTER_NULL`.
- Unknown categorical label in `Values` → `PROCESSING_CONFIG` at build time, surfaced via predict.
- Non-numeric values on a numeric field → `PROCESSING_CONFIG` (parse error).
- Filters chain in declared order; this one sees only rows the previous kept.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-exclude`
