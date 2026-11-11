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

| Name | Type | Default | Description |
|---|---|---|---|
| `ordered` | bool | `false` | Categorical field: dictionary order is the level order. |
| `levels` | array | — | Level order, lowest first, by label; implies `ordered`. |

Ordered (opt-in): a `categorical_*` field's levels by rank; without either key a categorical is refused.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper. Ordered: the highest level's dictionary index — a code, not a magnitude.

## Components

- `max` — Largest non-null value observed
- `n_unlisted` — ordered only: rows whose label `levels` omits

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `max`; optional `n_unlisted`.

## Gotchas

- Null rows skipped; `n_null` counts them.
- No non-null value in the bucket (group / cell / cohort) → `null` (NaN), components too.
- Ordered: an unlisted label is dropped (counted, not `n_null`); an overlay scoring the code is refused `categorical_code`, a later chain stage reading it `forwarded_code`.
- `PROCESSING_CONFIG` `reason`: `ordered_needs_categorical`, `unknown_level`, `duplicate_level` — predict reports each.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
