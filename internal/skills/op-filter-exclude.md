---
name: op-filter-exclude
description: Drop records whose field value appears in the supplied `values` list.
kind: operator
category: FILTER
operator: FILTER_EXCLUDE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Wire: `values` is a top-level filterer key (beside `field`), not under `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | array | (required) | Block-list of strings: labels on a categorical field, numbers otherwise. |

## Inputs

`field` — admitted types: see `## Contract`.

## Output

Row-level predicate. Drop when `field`'s value is in `values`; pass otherwise. No emitted column.

<!-- generated: contract -->

## Gotchas

- Null rows PASS this filter (an include filter drops them).
- Unknown categorical label in `values` → `PROCESSING_CONFIG` at build time, surfaced via predict.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-include`
