---
name: op-filter-null
description: Keep or drop records based on null state of a field.
kind: operator
category: FILTER
operator: FILTER_NULL
type: reference
applies_to: process, compose, predict
examples_tags: [data-quality, cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Wire: `values` is a top-level filterer key (beside `field`), not under `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | array | (required) | One entry: One entry: `"is_null"` keeps null-valued rows; `"is_not_null"` keeps non-null rows. Any other value → `PROCESSING_CONFIG`. |

## Inputs

- `field` — any cohort field type — null state is read from the per-record null bitmap.

## Output

Row-level predicate. Pass per the configured mode. No emitted column.

<!-- generated: contract -->

## Gotchas

- `field` is required; empty → `SERVICE_VALIDATION`. A missing or unknown `values` entry → `PROCESSING_CONFIG`, at predict and at run time.
- Reads the bitmap only — the underlying type's sentinel value (e.g. `NaN`, empty `set_*`) is NOT treated as null.
- For "non-null AND in a set", put this filter (`is_not_null`) before the value filter.

## See

- `pulse_examples_search tags=[data-quality]`
- Skills: `aggregation-design`, `response-components`, `cohort-schema-design`
