```yaml
name: op-agg-median
description: 50th percentile of the field; requires sorting the full value set.
kind: operator
category: AGG
operator: AGG_MEDIAN
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
```

## Use when

Middle value of a numeric field once sorted: the typical row, not pulled by a few extreme values.

Questions it answers:

- What is the typical household income in each region?
- What is the typical delivery time when a few deliveries take weeks?

Use something else:

- `AGG_PERCENTILE` when you want a value other than the middle, such as the 90th percentile.
- `AGG_MODE` when the field holds categories rather than numbers.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `ordered` | bool | `false` | Categorical field: dictionary order is the level order. |
| `levels` | array | — | Level order, lowest first, by label; implies `ordered`. |

Ordered: R `quantile(type = 1)` over level ranks — the lowest level whose cumulative share reaches ½ (even n: the lower middle); weighted, cumulative Σw ≥ ½Σw. No interpolation.

Weighted: Hmisc `wtd.quantile` — sort by value; h = 0.5·(Σw−1); x(k) = first value with cumulative Σw ≥ k+1 (snapped to an integer within 1e-9); interpolate x(⌊h⌋)..x(⌈h⌉). `probability` weights first rescaled to Σw = n (rows used) — scale-invariant; `frequency` weights stay raw = type 7 on duplicated rows.

## Inputs

`Field` — admitted types: see `## Contract`.

## Output

Scalar `float64`. Per-group when wired under a grouper. Ordered: the level's dictionary index — a code.

## Components

- `position_low` — Lower bracket index (weighted: ⌊h⌋, expanded)
- `position_high` — Upper bracket index (weighted: ⌈h⌉)
- `median` — Resolved median (linear interpolation)
- `n_unlisted` — ordered only: rows whose label `levels` omits (positions = the level's rank)

## Contract

- Types (`field`): `date`, `datetime`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`; `decimal128` is `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: buffered only; components `none`. Use AGG_AVERAGE for a streaming central-tendency proxy, or accept the buffered path.
- Components: `n`, `n_null`, `position_low`, `position_high`, `median`; optional `sum_weights`, `n_eff`, `n_weight_invalid`, `n_unlisted`.

## Gotchas

- Buffered-only path: full input materialised before sort.
- Outlier-robust, unlike the mean.
- No non-null value → `null` (NaN), never 0.
- Ordered: unlisted labels dropped; overlays refuse the code (`categorical_code`); `PROCESSING_CONFIG` `reason` `ordered_needs_categorical` / `unknown_level` / `duplicate_level`.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
