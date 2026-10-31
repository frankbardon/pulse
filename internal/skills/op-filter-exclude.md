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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Block-list. Categorical fields resolve labels through the dictionary; numeric fields parse each entry via `strconv.ParseFloat`. |

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Drop when `Field`'s value is in `Values`; pass otherwise. No emitted column.

<!-- generated: contract -->

## Gotchas

- Null rows PASS this filter (an include filter drops them).
- Unknown categorical label in `Values` → `PROCESSING_CONFIG` when the RUN builds the filter; predict does not check labels (`valid: true`), so read the dictionary (inspect) first.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-include`
