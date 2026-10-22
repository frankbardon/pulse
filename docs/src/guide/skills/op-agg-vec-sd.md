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
| `labels` | list | — | Inline `fields` only: one display label per member; absent: the member names. |
| `expand` | bool | — | `true`: one scalar column per member, `<label>[<element label>]`, instead of the array (grouped: per row); the label is absent. Predict lists them (`aggregations[i].columns`). Element labels must be distinct. |
| `element` | string | — | Crosstab `cell` / `margin_aggregations` only: one member's display label; the slot reads that member alone, a scalar (its sd). Refused with `expand`, outside a crosstab, unknown or shared label (`SERVICE_VALIDATION`, reason `element_*`). |

Slot: exactly one of `vector` (a `vectors` entry, its labels kept) / `fields`; no `field`. Default label `AGG_VEC_SD_<vector>`. Weight-aware: √(M2 / (N* − 1)) per member, M2 = Σw·(x − mean)², N* = Σw under `kind: frequency` (the expanded rows), Kish n_eff = (Σw)²/Σw² under `probability`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

Rich `[]float64`, one SAMPLE standard deviation per member (n − 1; `AGG_STDDEV` is the population form); per-element missing data (a null member skips only that element). An element with N* ≤ 1 — one answering row, none, or all weight on one row — is NaN (`null`), never 0.

## Reading the output

- `value`: One standard deviation per item, in member order, each in that item's own units: how far a typical row sits from the group's mean on that item. 0 means every row gave the item the same value.
  - Caveat: It is the sample form, dividing by n - 1 (AGG_STDDEV divides by n), so on a small group it reads larger than AGG_STDDEV of the same item.
  - Caveat: Each item counts only the rows that answered it; compare components element_n before comparing items.

## Components

- `labels`, `element_n` (raw per member), `element_sum`, `element_mean` (Welford running mean), `element_m2`; weighted adds `element_sum_weights`, `element_sum_weights_sq`.
- A row is present when ANY member answered. Chan combine per merge block; bit-identical for any worker count or shard split.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`, `labels`, `element_n`, `element_sum`, `element_mean`, `element_m2`; optional `sum_weights`, `n_eff`, `n_weight_invalid`, `element_sum_weights`, `element_sum_weights_sq`.

## Gotchas

- Array-valued: refused (`PROCESSING_CONFIG`, reason `array_valued`) as a crosstab cell or margin aggregation without `element`, a Sort key, a window or post-test input, the value an overlay scores (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]` — an unscored one beside it runs) and a chain stage feeding another unless expanded.
- Expanded columns `<label>[<elem>]` are ordinary nullable f64 columns: `sort`, a window, a post-test `field`, a series overlay's `params.field` and a later chain stage (NaN → null) read them. A `return` path names one quoted, `data[*]."<label>[<elem>]"`, or all with `data[*].<label>*`.
- `element` cell: floor and components are the member's; a record reaches an auxiliary margin only if its member reached a cell. Its margin is recomputed over raw rows, so the crosstab runs buffered.

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: [`op-agg-vec-mean`](op-agg-vec-mean.md), [`multivariate-design`](multivariate-design.md), [`op-agg-stddev`](op-agg-stddev.md)
