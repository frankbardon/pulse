```yaml
name: op-agg-range
description: Spread (max minus min) of the field across the input set.
kind: operator
category: AGG
operator: AGG_RANGE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, streaming-friendly]
```

## Use when

Distance between the smallest and largest value of a numeric field, over all rows or per group.

Questions it answers:

- How far apart are the cheapest and most expensive orders?
- How widely do delivery times span in each depot?

Use something else:

- `AGG_STDDEV` when you want a spread measure that uses every row, not only the two extremes.
- `AGG_PERCENTILE` when you want the spread of the middle of the data, ignoring extremes.

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — `max - min`. Per-group when wired under a grouper.

## Components

- `min` — Smallest non-null value
- `max` — Largest non-null value

## Contract

- Types (`field`): `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `min`, `max`.

## Gotchas

- Outlier-sensitive — one extreme blows the spread.
- No non-null value in the bucket (group / cell / cohort) → `null` (NaN), components too.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
