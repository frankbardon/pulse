```yaml
name: op-agg-vec-sd
description: Per-member sample standard deviation of a vector — one spread per member, in member order (how much a group disagrees on each item).
kind: operator
category: AGG
operator: AGG_VEC_SD
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, streaming-friendly]
```

## Use when

Standard deviation of each item of a battery or mix, as one list per group: how much the group's rows disagree on each item.

Questions it answers:

- On which of the eight brand attributes do each segment's ratings disagree most?
- How much does each sales channel's share vary between stores in each region?

Use something else:

- `AGG_VEC_MEAN` when you want each item's average rather than its spread.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `labels` | list | — | Inline `fields`: one display label per member. |
| `expand` | bool | — | One scalar column `<label>[<element label>]` per member instead of the array. |
| `element` | string | — | Crosstab cell / margin only: one member by label, a scalar (its sd). |

Slot: `vector` or `fields`, no `field` ([`vector-aggregates`](vector-aggregates.md)). Default label `AGG_VEC_SD_<vector>`. Weight-aware: √(M2 / (N* − 1)) per member, M2 = Σw·(x − mean)², N* = Σw under `kind: frequency` (the expanded rows), Kish n_eff = (Σw)²/Σw² under `probability`.

## Inputs

Members: see `## Contract`; source columns only.

## Output

Rich `[]float64`, one SAMPLE standard deviation per member (n − 1; `AGG_STDDEV` is the population form); a null member skips only that element. An element with N* ≤ 1 — one answering row, none, or all weight on one row — is NaN (`null`), never 0.

## Reading the output

- `value`: One standard deviation per item, in member order, each in that item's own units: how far a typical row sits from the group's mean on that item. 0 means every row gave the item the same value.
  - Caveat: It is the sample form, dividing by n - 1 (AGG_STDDEV divides by n), so on a small group it reads larger than AGG_STDDEV of the same item.
  - Caveat: Each item counts only the rows that answered it; compare components element_n before comparing items.

## Components

- `labels`, `element_n` (raw per member), `element_sum`, `element_mean` (Welford running mean), `element_m2`; weighted adds `element_sum_weights`, `element_sum_weights_sq`.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `labels`, `element_n`, `element_sum`, `element_mean`, `element_m2`; optional `sum_weights`, `n_eff`, `n_weight_invalid`, `element_sum_weights`, `element_sum_weights_sq`.

## Gotchas

- The array is refused (`PROCESSING_CONFIG`, reason `array_valued`) as a Sort key, a window, post-test or overlay input, a crosstab cell or margin without `element`, or a chain stage feeding another — unless expanded ([`vector-aggregates`](vector-aggregates.md)).
- Expanded columns are ordinary nullable f64 columns; a `return` path names one quoted, `data[*]."<label>[<elem>]"`.
- `element` cell: a record reaches an auxiliary margin only if its member reached a cell.

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: [`vector-aggregates`](vector-aggregates.md), [`op-agg-vec-mean`](op-agg-vec-mean.md), [`multivariate-design`](multivariate-design.md), [`op-agg-stddev`](op-agg-stddev.md)
