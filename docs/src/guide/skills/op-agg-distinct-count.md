```yaml
name: op-agg-distinct-count
description: Count distinct non-null values across the input set.
kind: operator
category: AGG
operator: AGG_DISTINCT_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, streaming-friendly]
```

## Use when

Number of different values a field takes, over all rows or per group.

Questions it answers:

- How many different customers ordered this month?
- Does the respondent ID appear once per row, or are there duplicates?

Use something else:

- `AGG_COUNT` when you want how many rows there are, not how many different values.
- `GROUP_CATEGORY` when you want how many rows hold each value: group by the field, then count.

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type EXCEPT `set_*` (categorical_*, numeric, date, datetime, packed_bool, decimal128) |

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
- `set_*` rejected at build time with `PROCESSING_CONFIG`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
