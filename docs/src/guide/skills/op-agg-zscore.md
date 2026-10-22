```yaml
name: op-agg-zscore
description: Standardized z-score aggregate — mean-centered, stddev-scaled summary.
kind: operator
category: AGG
operator: AGG_ZSCORE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, comparison]
```

## Use when

Population mean and standard deviation of a numeric field, for standardizing it; the value itself is always 0.

Questions it answers:

- What centre and scale standardize this field in each group?
- How many standard deviations does the group's last row (in row order) sit from the group's average, that row included?

Use something else:

- `ATTR_ZSCORE` when you want a z-score on every row.

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — the MEAN of the group's row z-scores, `0` by construction. The usable figures are the components; `zscore` there is the LAST row's `(target_value - pop_mean) / pop_stddev`. Per-group when wired under a grouper.

## Reading the output

- `value`: The average of every row's z-score within the group, which is 0 by construction (up to rounding): the value carries no information about the data. The usable numbers are in the components: pop_mean and pop_stddev (the centre and the population spread, dividing by n), target_value (the LAST row's value) and zscore (that row's z-score).
  - Caveat: For a z-score on every row use ATTR_ZSCORE; for each group's value against all groups use OVERLAY_ZSCORE_VS_TOTAL.

## Components

- `pop_mean` — Population mean (center)
- `pop_stddev` — Population stddev (scale)
- `target_value` — Value standardized
- `zscore` — Resolved score

## Contract

- Types (`field`): `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: buffered only; components `mergeable`. Streaming uses online Welford moments; the ZSCORE aggregate finalize step needs the full deviation sum.
- Components: `n`, `n_null`, `pop_mean`, `pop_stddev`, `target_value`, `zscore`.

## Gotchas

- Buffered path only.
- Empty group → `null` (NaN), scalar and components; zero stddev → scalar and `zscore` `null` (0/0), `pop_mean` / `pop_stddev` (0) / `target_value` defined.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`attribute-composition`](attribute-composition.md), [`response-components`](response-components.md)
