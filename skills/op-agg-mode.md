---
name: op-agg-mode
description: Most-frequent value of the field (ties broken by first-seen order).
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
| `Field` | any cohort field type (categorical_*, numeric, date, packed_bool, set_*, decimal128) |

## Output

`string` — echoes the dictionary value or stringified scalar. Per-group when wired under a grouper.

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

- First-seen tie-break — order-sensitive when ties present (`tie_count > 0` flags it).
- High-cardinality fields blow memory; pre-filter or use `AGG_DISTINCT_COUNT`.
- For the full histogram use `AGG_FREQUENCY`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `response-components`
