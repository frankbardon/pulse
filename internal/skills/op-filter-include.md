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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[]string` | (required) | Allow-list. Categorical fields resolve labels through the dictionary; numeric fields parse each entry via `strconv.ParseFloat`. |

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Pass when `Field`'s value is in `Values`; drop otherwise. No emitted column.

<!-- generated: contract -->

## Gotchas

- Null rows fail the predicate (dropped).
- Unknown categorical label in `Values` → `PROCESSING_CONFIG` at build time, surfaced via predict.
- Non-numeric values on a numeric field → `PROCESSING_CONFIG` (parse error), surfaced via predict.
- Filters chain in declared order; this one sees only rows the previous kept.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-exclude`
