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

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `decimal128`, `date`, `datetime`, `packed_bool`, `u4` |

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `max` | float64 | Largest non-null value observed |

- Mergeability: `Mergeable`
- Streaming: per-chunk emits running max

## Gotchas

- Null rows skipped; `n_null` counts them.
- All-null cohort → emits NaN.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
