---
name: op-agg-mode
description: Most-frequent value of the field (ties broken by the smallest value).
kind: operator
category: AGG
operator: AGG_MODE
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cross-tabulation]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type EXCEPT `set_*` (categorical_*, numeric, date, datetime, packed_bool, decimal128) |

## Output

Scalar `float64` — the modal value (categorical: its dictionary index). Per-group under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `value` | any | Most-frequent value |
| `count` | int | Row count of the modal value |
| `distinct_count` | int | Distinct values observed |
| `tie_count` | int | Values tied at the max count |

- Mergeability: `Partial` — map allocation
- Streaming: per-chunk per-value counter merged at flush

## Gotchas

- Ties go to the SMALLEST value (or index), not the first seen; `tie_count > 1` flags a tie.
- High-cardinality fields blow memory; pre-filter or use `AGG_DISTINCT_COUNT`.
- Per-value counts: `GROUP_CATEGORY` + `AGG_COUNT`.
- `set_*` → `PROCESSING_CONFIG` (no modal scalar); use `AGG_SET_DISTINCT_VALUES` / `AGG_SET_FREQUENCY`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `response-components`
