---
name: op-agg-vec-mean
description: Per-member mean of a vector — one average per member, in member order (a group's average profile).
kind: operator
category: AGG
operator: AGG_VEC_MEAN
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, streaming-friendly]
---

<!-- generated: use-when -->

## Params

- `labels` — inline `fields` only: one display label per member (default the member names).
- `expand` — `true`: one scalar column per member, `<label>[<element label>]`, instead of the array (grouped: per row); the label is absent. Predict lists them (`aggregations[i].columns`). Element labels must be distinct.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry, its labels kept)<!-- /feature --> / `fields`; no `field`. Default label `AGG_VEC_MEAN_<vector>`.<!-- feature: capability:weighting --> Weight-aware: Σw·x / Σw per member.<!-- /feature -->

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

Rich `[]float64`, one mean per member; per-element missing data (a null member skips only that element). An element no row answered is NaN (`null`).

## Components

Floor `{n, n_null}` — a row is present when ANY member answered<!-- feature: capability:weighting --> (weighted adds `sum_weights`, `n_eff`, `n_weight_invalid`)<!-- /feature -->. Plus `labels`, `element_n` (raw per member), `element_sum`<!-- feature: capability:weighting -->, `element_sum_weights` (weighted)<!-- /feature -->. `Mergeable`; streams; bit-identical for any worker count or shard split.

## Gotchas

- Array-valued: refused (`PROCESSING_CONFIG`, reason `array_valued`) as a crosstab cell, a Sort key, a window or post-test input, the value an overlay scores (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]` — an unscored one beside it runs) and a chain stage feeding another.
- Expanded columns `<label>[<elem>]` are ordinary nullable f64 columns: `sort`, a window (`field` / `order_by` / `partition_by`), a post-test `field` and a series overlay's `params.field` read them; a feeding chain stage still refuses them. A `return` path cannot name a bracketed column: keep them with `data[*].<label>*`.

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: `op-agg-vec-sum`, `op-attr-scale-score`, `multivariate-design`
