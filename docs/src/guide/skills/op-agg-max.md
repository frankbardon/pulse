```yaml
name: op-agg-max
description: Largest non-null value of the field.
kind: operator
category: AGG
operator: AGG_MAX
type: reference
applies_to: process, compose, predict
examples_tags: [financial, streaming-friendly]
```

## Use when

Largest value of a numeric or date field, over all rows or per group.

Questions it answers:

- What is the latest order date in each region?
- Is any order value implausibly large?

Use something else:

- `AGG_MIN` when you want the smallest value.
- `AGG_PERCENTILE` when one extreme row would mislead and you want a high value that ignores it.
- `AGG_RANGE` when you want the distance between smallest and largest.

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

- `max` — Largest non-null value observed

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `max`.

## Gotchas

- Null rows skipped; `n_null` counts them.
- No non-null value in the bucket (group / cell / cohort) → `null` (NaN), components too.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
