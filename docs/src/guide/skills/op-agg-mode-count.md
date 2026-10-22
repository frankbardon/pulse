```yaml
name: op-agg-mode-count
description: Modal count — how many rows hold the field's most common value; one float64 per output row.
kind: operator
category: AGG
operator: AGG_MODE_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [cross-tabulation, cardinality-analysis]
```

## Use when

How many rows share a field's most common value, over all rows or per group.

Questions it answers:

- How many respondents gave the most common answer?
- How many orders used the most popular payment method?

Use something else:

- `GROUP_CATEGORY` when you want a count for every value of the field: group by it, then count rows.
- `capability:facet` when you want per-value counts for several fields in one call.

## Params

Weighted: the largest per-value Σw (a float), its value the weighted mode.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — the modal count, NOT a per-value map. Per group under a grouper; grouped by category on the same field it equals each group's row count.

## Components

- `distinct_count` — Number of distinct values
- `mode_value` — Modal value (smallest wins a tie)
- `mode_count` — Row count of the modal value (= scalar; weighted: Σw)

## Contract

- Types (`field`): `categorical_*`, `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `partial`.
- Components: `n`, `n_null`, `distinct_count`, `mode_value`, `mode_count`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Smart default for categorical_* and packed_bool. Memory grows with distinct values.
- Crosstab margin = modal count of the margin's own rows, never a sum of cells (class `independent`: fuses).

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
