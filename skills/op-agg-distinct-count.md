---
name: op-agg-distinct-count
description: Count distinct non-null values across the input set.
kind: operator
category: AGG
operator: AGG_DISTINCT_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
---

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type (categorical_*, numeric, date, packed_bool, set_*, decimal128) |

## Output

Scalar `int64` — number of distinct non-null values. Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `cardinality` | int | Distinct non-null values observed |

- Mergeability: `Partial` — distinct-set merge
- Streaming: per-chunk; merge unions per-chunk seen-sets

## Gotchas

- High-cardinality fields → memory growth proportional to distinct values.
- Counts non-null only; nulls collapsed.
- For exact-mask distinct counts on `set_*` use `AGG_SET_DISTINCT_VALUES`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `response-components`
