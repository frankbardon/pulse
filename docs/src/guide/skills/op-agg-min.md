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

| Name | Type | Default | Description |
|---|---|---|---|
| `ordered` | bool | `false` | Categorical field: dictionary order is the level order. |
| `levels` | array | — | Level order, lowest first, by label; implies `ordered`. |

Ordered (opt-in): a `categorical_*` field's levels by rank; without either key a categorical is refused.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper. Ordered: the lowest level's dictionary index — a code, not a magnitude.

## Components

- `min` — Smallest non-null value observed
- `n_unlisted` — ordered only: rows whose label `levels` omits

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `min`; optional `n_unlisted`.

## Gotchas

- Null rows skipped; `n_null` counts them.
- No non-null value in the bucket (group / cell / cohort) → `null` (NaN), components too.
- Decimal128 preserved on input; float64 output coerces the comparison space.
- Ordered: an unlisted label is dropped (counted, not `n_null`); an overlay scoring the code is refused `categorical_code`, a later chain stage reading it `forwarded_code`.
- `PROCESSING_CONFIG` `reason`: `ordered_needs_categorical`, `unknown_level`, `duplicate_level` — predict reports each.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
