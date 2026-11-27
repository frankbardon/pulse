---
name: op-filter-include
description: Keep records whose field value appears in the supplied `values` list.
kind: operator
category: FILTER
operator: FILTER_INCLUDE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Wire: `values` is a top-level filterer key (beside `field`), not under `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | array | (required) | Allow-list of strings: labels on a categorical field, numbers otherwise. |

## Inputs

`field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Pass when `field`'s value is in `values`; drop otherwise. No emitted column.

<!-- generated: contract -->

## Gotchas

- Null rows fail the predicate (dropped).
- Unknown categorical label in `values` → `PROCESSING_CONFIG` at build time, surfaced via predict.
- Non-numeric values on a numeric field → `PROCESSING_CONFIG` (parse error), surfaced via predict.
- Filters chain in declared order; this one sees only rows the previous kept.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-exclude`
