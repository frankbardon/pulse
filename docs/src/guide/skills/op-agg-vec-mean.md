```yaml
name: op-agg-vec-mean
description: Per-member mean of a vector — one average per member, in member order (a group's average profile).
kind: operator
category: AGG
operator: AGG_VEC_MEAN
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, streaming-friendly]
```

## Use when

Average of each item of a battery or mix, as one list per group: the group's average profile across the items.

Questions it answers:

- What is each segment's average rating on each of the eight brand attributes?
- What does the typical store's channel mix look like in each region?

Use something else:

- `ATTR_SCALE_SCORE` when you want one score per row from the items.
- `AGG_AVERAGE` when you want the average of a single field.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `labels` | list | — | Inline `fields`: one display label per member. |
| `expand` | bool | — | One scalar column `<label>[<element label>]` per member instead of the array. |
| `element` | string | — | Crosstab cell / margin only: one member by label, a scalar (its mean). |

Slot: `vector` or `fields`, no `field` ([`vector-aggregates`](vector-aggregates.md)). Default label `AGG_VEC_MEAN_<vector>`. Weight-aware: Σw·x / Σw per member.

## Inputs

Members: see `## Contract`; source columns only.

## Output

Rich `[]float64`, one mean per member; a null member skips only that element. An element no row answered is NaN (`null`).

## Components

- `labels`, `element_n` (raw per member), `element_sum`; weighted adds `element_sum_weights`.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `labels`, `element_n`, `element_sum`; optional `sum_weights`, `n_eff`, `n_weight_invalid`, `element_sum_weights`.

## Gotchas

- The array is refused (`PROCESSING_CONFIG`, reason `array_valued`) as a Sort key, a window, post-test or overlay input, a crosstab cell or margin without `element`, or a chain stage feeding another — unless expanded ([`vector-aggregates`](vector-aggregates.md)).
- Expanded columns are ordinary nullable f64 columns; a `return` path names one quoted, `data[*]."<label>[<elem>]"`.
- `element` cell: a record reaches an auxiliary margin only if its member reached a cell.

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: [`vector-aggregates`](vector-aggregates.md), [`op-agg-vec-sum`](op-agg-vec-sum.md), [`op-agg-vec-sd`](op-agg-vec-sd.md), [`op-attr-scale-score`](op-attr-scale-score.md), [`multivariate-design`](multivariate-design.md)
