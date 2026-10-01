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

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[1]string` | (required) | `"is_null"` keeps null-valued rows; `"is_not_null"` keeps non-null rows. Any other value → `PROCESSING_CONFIG`. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type — null state is read from the per-record null bitmap. |

## Output

Row-level predicate. Pass per the configured mode. No emitted column.

## Components

Floor only — no operator-specific keys. Universal `{n_in, n_out, n_null_input}` per `response-components` contract. `n_null_input` matches `n_out` when mode is `is_null`, and `n_in − n_out` when mode is `is_not_null` (book-keeping mirrors the predicate). Mergeable across chunks.

## Gotchas

- `Field` is required; empty → `PROCESSING_CONFIG`.
- Reads the bitmap only — the underlying type's sentinel value (e.g. `NaN`, empty `set_*`) is NOT treated as null.
- For "non-null AND in set" chain `FILTER_NULL` (`is_not_null`) before `FILTER_INCLUDE`.

## See

- `pulse_examples_search tags=[data-quality]`
- Skills: `aggregation-design`, `response-components`, `cohort-schema-design`
