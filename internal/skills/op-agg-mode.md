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
| `Field` | any field type EXCEPT `set_*` |

## Output

Scalar `float64` — the modal value (categorical: its dictionary index). Per-group under a grouper. ProcessChain admits it: a later stage reads that index as a number, not a label.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `value` | any | Most-frequent value |
| `count` | int | Row count of the modal value |
| `distinct_count` | int | Distinct values observed |
| `tie_count` | int | Values tied at the max count |

- Mergeability: `Partial` — map allocation
- Streaming: per-value counts merged at flush

## Gotchas

- Ties go to the SMALLEST value (or index), not the first seen; `tie_count > 1` flags a tie.
- High cardinality costs memory; pre-filter.
- Per-value counts: `GROUP_CATEGORY` + `AGG_COUNT`; one chosen value's count: `AGG_FREQUENCY`.
- `set_*` → `PROCESSING_CONFIG` (no modal scalar); use `AGG_SET_DISTINCT_VALUES` / `AGG_SET_FREQUENCY`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `response-components`
