---
name: op-filter-exclude
description: Drop records whose field value appears in the supplied Values list.
kind: operator
category: FILTER
operator: FILTER_EXCLUDE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Block-list. Categorical fields resolve labels through the dictionary; numeric fields parse each entry via `strconv.ParseFloat`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type (numeric, categorical_*, date, packed_bool, decimal128) |

## Output

Row-level predicate. Drop when `Field`'s value is in `Values`; pass otherwise. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. Mergeable across chunks; counters fold by simple addition.

## Gotchas

- Null rows PASS this filter (asymmetric vs `FILTER_INCLUDE`). For null-aware logic use `FILTER_NULL`.
- Unknown categorical label in `Values` → `PROCESSING_CONFIG` at build time, surfaced via predict.
- For "everything not in this small set" prefer `FILTER_INCLUDE` against the complement when the dictionary is small.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-include`
