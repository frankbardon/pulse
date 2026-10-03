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

`AGG_COUNT` counts non-null rows.

## Output

Scalar `int64`. Per-group when wired under a grouper; otherwise one row across the cohort.

## Components

Floor only — no operator-specific keys. Universal `{n, n_null}` per response-components contract.

- Mergeability: `Mergeable`
- Streaming: per-chunk emission; orchestrator sums

## Gotchas

- Counts non-null inputs only.
- Never a smart default: an omitted `Type` never infers `AGG_COUNT` — name it explicitly.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
