---
name: op-agg-vec-sd
description: Per-member sample standard deviation of a vector — one spread per member, in member order (how much a group disagrees on each item).
kind: operator
category: AGG
operator: AGG_VEC_SD
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
| `element` | string | — | Crosstab `cell` / `margin_aggregations` only: one member's display label; the slot reads that member alone, a scalar (its sd). Refused with `expand`, outside a crosstab, unknown or shared label (`SERVICE_VALIDATION`, reason `element_*`). |
<!-- /feature -->

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry, its labels kept)<!-- /feature --> / `fields`; no `field`. Default label `AGG_VEC_SD_<vector>`.<!-- feature: capability:weighting --> Weight-aware: √(M2 / (N* − 1)) per member, M2 = Σw·(x − mean)², N* = Σw under `kind: frequency` (the expanded rows), Kish n_eff = (Σw)²/Σw² under `probability`.<!-- /feature -->

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

Rich `[]float64`, one SAMPLE standard deviation per member (n − 1<!-- feature: AGG_STDDEV -->; `AGG_STDDEV` is the population form<!-- /feature -->); per-element missing data (a null member skips only that element). An element with N* ≤ 1 — one answering row, none, or all weight on one row — is NaN (`null`), never 0.

<!-- generated: reading-the-output -->

## Components

- `labels`, `element_n` (raw per member), `element_sum`, `element_mean` (Welford running mean), `element_m2`; weighted adds `element_sum_weights`, `element_sum_weights_sq`.
- A row is present when ANY member answered. Chan combine per merge block; bit-identical for any worker count or shard split.

<!-- generated: contract -->

## Gotchas

- Array-valued: refused (`PROCESSING_CONFIG`, reason `array_valued`) as <!-- feature: capability:crosstab -->a crosstab cell or margin aggregation without `element`, <!-- /feature -->a Sort key, a window or post-test input, the value an overlay scores (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]` — an unscored one beside it runs) and a chain stage feeding another unless expanded.
- Expanded columns `<label>[<elem>]` are ordinary nullable f64 columns: `sort`, a window, a post-test `field`, a series overlay's `params.field` and a later chain stage (NaN → null) read them. A `return` path names one quoted, `data[*]."<label>[<elem>]"`, or all with `data[*].<label>*`.
- <!-- feature: capability:crosstab -->`element` cell: floor and components are the member's; a record reaches an auxiliary margin only if its member reached a cell. Its margin is recomputed over raw rows, so the crosstab runs buffered.<!-- /feature -->

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: `op-agg-vec-mean`, `multivariate-design`<!-- feature: AGG_STDDEV -->, `op-agg-stddev`<!-- /feature -->
