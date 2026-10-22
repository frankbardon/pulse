---
name: op-agg-vec-sum
description: Per-member total of a vector — one sum per member, in member order.
kind: operator
category: AGG
operator: AGG_VEC_SUM
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, streaming-friendly]
---

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `labels` | list | — | Inline `fields` only: one display label per member; absent: the member names. |
| `expand` | bool | — | `true`: one scalar column per member, `<label>[<element label>]`, instead of the array (grouped: per row); the label is absent. Predict lists them (`aggregations[i].columns`). Element labels must be distinct. |
<!-- feature: capability:crosstab -->
| `element` | string | — | Crosstab `cell` / `margin_aggregations` only: one member's display label; the slot reads that member alone, a scalar (its total). Refused with `expand`, outside a crosstab, unknown or shared label (`SERVICE_VALIDATION`, reason `element_*`). |
<!-- /feature -->

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry, its labels kept)<!-- /feature --> / `fields`; no `field`. Default label `AGG_VEC_SUM_<vector>`.<!-- feature: capability:weighting --> Weight-aware: Σw·x per member.<!-- /feature -->

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

Rich `[]float64`, one total per member; per-element missing data (a null member skips only that element). An element no row answered totals 0.

## Components

- `labels`, `element_n` (raw per member), `element_sum`; weighted adds `element_sum_weights`.
- A row is present when ANY member answered. Bit-identical for any worker count or shard split.

<!-- generated: contract -->

## Gotchas

- Array-valued: refused (`PROCESSING_CONFIG`, reason `array_valued`) as <!-- feature: capability:crosstab -->a crosstab cell or margin aggregation without `element`, <!-- /feature -->a Sort key, a window or post-test input, the value an overlay scores (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]` — an unscored one beside it runs) and a chain stage feeding another unless expanded.
- Expanded columns `<label>[<elem>]` are ordinary nullable f64 columns: `sort`, a window (`field` / `order_by` / `partition_by`), a post-test `field` and a series overlay's `params.field` and a later chain stage (as f64 input fields; NaN → null) read them. A `return` path names one quoted, `data[*]."<label>[<elem>]"`, or all with `data[*].<label>*`.
- <!-- feature: capability:crosstab -->`element` cell: floor and components are the member's (one-entry arrays); a record reaches an auxiliary margin only if its member reached a cell. Fuses like the scalar twin.<!-- /feature -->

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: `op-agg-vec-mean`, `op-agg-vec-sd`, `multivariate-design`
