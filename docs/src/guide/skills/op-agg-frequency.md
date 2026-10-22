```yaml
name: op-agg-frequency
description: Value count — how many non-null rows hold one chosen value (params.value); one float64 per output row.
kind: operator
category: AGG
operator: AGG_FREQUENCY
type: reference
applies_to: process, compose, predict
examples_tags: [cross-tabulation, streaming-friendly]
```

## Use when

How many rows hold one chosen value of a field, such as the Yes answers, over all rows or per group.

Questions it answers:

- How many respondents answered Yes in each region?
- How many orders were returned each day?

Use something else:

- `AGG_MODE_COUNT` when you want how many rows share the field's most common value, whatever it is.
- `FILTER_INCLUDE` when you want rows holding any of several values: keep them, then count rows.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `value` | string | (required) | Matched like an include filter: category label, else a number (`date` days, `datetime` seconds, `packed_bool` `1`/`0`) |

Weighted: Σw of matches (a float); `share` = Σw_match/Σw.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64` — rows equal to `value`, per group.

## Components

- `match_count` — Rows equal to `value` (= scalar; weighted: Σw)
- `share` — `match_count / n`; omitted when `n` is 0

## Contract

- Types (`field`): `categorical_*`, `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `match_count`, `share`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Missing `value` → `PROCESSING_CONFIG` (runtime and predict).
- A value no row holds counts 0, not an error.
- Nulls: not counted, not in `share`'s base.
- Weighted Σw figures are binned sums: the same bits on every worker count and shard split.

## See

- `pulse_examples_search tags=[cross-tabulation]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
