```yaml
name: op-agg-percentile
description: Configurable percentile of the field; requires sorting the full value set.
kind: operator
category: AGG
operator: AGG_PERCENTILE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
```

## Use when

Value of a numeric field below which a chosen share of rows fall, such as the 90th percentile of delivery time.

Questions it answers:

- How long do the slowest 10% of deliveries take in each region?
- What income marks the top quarter of respondents?

Use something else:

- `AGG_MEDIAN` when you want the 50th percentile.
- `ATTR_PERCENTILE` when you want each row's own percentile position.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `percentile` | float | (required) | Percentile in `[0, 100]`. e.g. 95 for p95. |

Weighted: Hmisc `wtd.quantile` — sort by value; h = p·(Σw−1); x(k) = first value with cumulative Σw ≥ k+1 (snapped to an integer within 1e-9); interpolate x(⌊h⌋)..x(⌈h⌉). `probability` weights first rescaled to Σw = n (rows used) — scale-invariant; `frequency` weights stay raw = type 7 on duplicated rows.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper.

## Reading the output

- `value`: The value below which the requested share of the group's values fall, in the field's own units: at percentile 90 about 90% of values sit at or below it. Pulse interpolates linearly between the two nearest sorted values (R's default, type 7), so it may be a value no row holds.
  - Caveat: Tools use different interpolation rules, so on small groups another tool can return a somewhat different value for the same percentile.
  - Caveat: A percentile near 0 or 100 on a small group rests on one or two rows and moves a lot when they change.

## Components

- `p` — Requested percentile
- `position` — Index in sorted set (weighted: ⌊h⌋, expanded)
- `lower` — Lower bracket value
- `upper` — Upper bracket value
- `method` — Interpolation method (e.g. `"linear"`)
- `value` — Resolved percentile

## Contract

- Types (`field`): `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: buffered only; components `none`. Use AGG_AVERAGE or accept the buffered path; exact percentiles need sorted input.
- Components: `n`, `n_null`, `p`, `position`, `lower`, `upper`, `method`, `value`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Buffered full-input path; cohort-sized memory peak.
- Out-of-range `percentile` rejected.
- No non-null value → `null` (NaN), never 0.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
