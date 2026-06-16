---
name: op-agg-frequency
description: Per-distinct-value count of the field; returned as map[string]int64.
kind: operator
category: AGG
operator: AGG_FREQUENCY
type: reference
applies_to: process, compose, predict
examples_tags: [cross-tabulation, cardinality-analysis]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type (categorical_*, numeric, date, packed_bool, set_*, decimal128) |

## Output

`map[string]int64` — keyed by stringified value. Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `distinct_count` | int | Number of distinct values |
| `mode_value` | any | Most-frequent value (first-seen tie-break) |
| `mode_count` | int | Row count of the modal value |

- Mergeability: `Partial` — map allocation, orchestrator may stage at flush
- Streaming: per-chunk maps merged bin-by-bin

## Gotchas

- Smart default for categorical_* and packed_bool fields.
- High-cardinality fields blow memory — pair with `FILTER_INCLUDE` first or use `AGG_DISTINCT_COUNT`.
- `mode_value` returned in Components; for the mode alone use `AGG_MODE`.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: `aggregation-design`, `response-components`
