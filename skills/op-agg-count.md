---
name: op-agg-count
description: Count records that pass the active filter, optionally per group.
kind: operator
category: AGG
operator: AGG_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, cohort-analysis]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type (numeric, categorical, date, packed_bool, set_*, decimal128) |

`AGG_COUNT` counts non-null rows. For null rows use `AGG_NULL_COUNT`.

## Output

Scalar `int64`. Per-group when wired under a grouper; otherwise one row across the cohort.

## Components

Floor only — no operator-specific keys. Universal `{n, n_null}` per response-components contract.

- Mergeability: `Mergeable`
- Streaming: per-chunk emission; orchestrator sums

## Gotchas

- Counts non-null inputs only; use `AGG_NULL_COUNT` for the inverse.
- Smart-default aggregator for numeric/categorical fields when `Type` is omitted is `AGG_SUM`/`AGG_FREQUENCY`, NOT `AGG_COUNT`.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
