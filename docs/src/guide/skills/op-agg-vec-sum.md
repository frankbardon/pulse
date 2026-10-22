```yaml
name: op-agg-vec-sum
description: Per-member total of a vector — one sum per member, in member order.
kind: operator
category: AGG
operator: AGG_VEC_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, streaming-friendly]
```

## Use when

Total of each item of a set of related fields, as one list per group.

Questions it answers:

- How many units of each of the six products did each store sell?
- What is the total spend per channel for every region, in one list?

Use something else:

- `AGG_VEC_MEAN` when you want each item's average rather than its total.
- `AGG_SUM` when you want the total of a single field.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `labels` | list | — | Inline `fields` only: one display label per member; absent: the member names. |
| `expand` | bool | — | `true`: one scalar column per member, `<label>[<element label>]`, instead of the array (grouped: per row); the label is absent. Predict lists them (`aggregations[i].columns`). Element labels must be distinct. |
| `element` | string | — | Crosstab `cell` / `margin_aggregations` only: one member's display label; the slot reads that member alone, a scalar (its total). Refused with `expand`, outside a crosstab, unknown or shared label (`SERVICE_VALIDATION`, reason `element_*`). |

Slot: exactly one of `vector` (a `vectors` entry, its labels kept) / `fields`; no `field`. Default label `AGG_VEC_SUM_<vector>`. Weight-aware: Σw·x per member.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

Rich `[]float64`, one total per member; per-element missing data (a null member skips only that element). An element no row answered totals 0.

## Components

- `labels`, `element_n` (raw per member), `element_sum`; weighted adds `element_sum_weights`.
- A row is present when ANY member answered. Bit-identical for any worker count or shard split.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `labels`, `element_n`, `element_sum`; optional `sum_weights`, `n_eff`, `n_weight_invalid`, `element_sum_weights`.

## Gotchas

- Array-valued: refused (`PROCESSING_CONFIG`, reason `array_valued`) as a crosstab cell or margin aggregation without `element`, a Sort key, a window or post-test input, the value an overlay scores (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]` — an unscored one beside it runs) and a chain stage feeding another unless expanded.
- Expanded columns `<label>[<elem>]` are ordinary nullable f64 columns: `sort`, a window (`field` / `order_by` / `partition_by`), a post-test `field` and a series overlay's `params.field` and a later chain stage (as f64 input fields; NaN → null) read them. A `return` path names one quoted, `data[*]."<label>[<elem>]"`, or all with `data[*].<label>*`.
- `element` cell: floor and components are the member's (one-entry arrays); a record reaches an auxiliary margin only if its member reached a cell. Fuses like the scalar twin.

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: [`op-agg-vec-mean`](op-agg-vec-mean.md), [`op-agg-vec-sd`](op-agg-vec-sd.md), [`multivariate-design`](multivariate-design.md)
