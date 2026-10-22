```yaml
name: op-agg-min
description: Smallest non-null value of the field.
kind: operator
category: AGG
operator: AGG_MIN
type: reference
applies_to: process, compose, predict
examples_tags: [financial, streaming-friendly]
```

## Use when

Smallest value of a numeric or date field, over all rows or per group.

Questions it answers:

- What is the earliest order date in each region?
- Are there negative or impossible values in the age field?

Use something else:

- `AGG_MAX` when you want the largest value.
- `AGG_PERCENTILE` when one extreme row would mislead and you want a low value that ignores it.

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

- `min` — Smallest non-null value observed

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `min`.

## Gotchas

- Null rows skipped; `n_null` counts them.
- No non-null value in the bucket (group / cell / cohort) → `null` (NaN), components too.
- Decimal128 preserved on input; float64 output coerces the comparison space.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
