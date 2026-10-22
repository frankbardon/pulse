```yaml
name: op-agg-average
description: Arithmetic mean of a numeric field across the input set.
kind: operator
category: AGG
operator: AGG_AVERAGE
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, data-quality]
```

## Use when

Average of a numeric field, over all rows or per group.

Questions it answers:

- What is the average order value?
- What is the typical rating in each region?

Use something else:

- `AGG_MEDIAN` when the field is skewed or has extreme values and you want the typical row.
- `AGG_WEIGHTED_MEAN` when rows carry weights.
- `AGG_WELFORD` when you also need the spread around the average.

## Params

Weighted: Σw·x / Σw.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Components

- `sum` — Running sum; mean = sum / n.
- Weighted adds `sum_weighted`, `weighted_mean`, `m2_weighted`, `sum_weights_sq`, `weighted_variance` (m2/(Σw−1); `null` if Σw ≤ 1).

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `sum`; optional `sum_weights`, `n_eff`, `n_weight_invalid`, `sum_weighted`, `weighted_mean`, `m2_weighted`, `sum_weights_sq`, `weighted_variance`.

## Gotchas

- Null inputs skipped — mean is over `n` non-null, not the cohort.
- Binned sum over exact `n`: the same bits on every worker count and shard split; weighted `m2_weighted` / `weighted_variance` are Welford — within ULP, not bitwise.
- No non-null value in the bucket (or Σw = 0) → `null` (NaN), never 0.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
