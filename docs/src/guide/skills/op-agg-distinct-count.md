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

`Field` — admitted types: see `## Contract`.

## Output

Scalar `int64` — number of distinct non-null values. Per-group when wired under a grouper.

## Components

- `cardinality` — Distinct non-null values observed

## Contract

- Types (`field`): `categorical_*`, `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `partial`.
- Components: `n`, `n_null`, `cardinality`.

## Gotchas

- High-cardinality fields → memory growth proportional to distinct values.
- Counts non-null only; nulls collapsed.
- Over `decimal128` the count is a JSON integer (`3`), never a decimal string; distinct by value at the column scale.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
