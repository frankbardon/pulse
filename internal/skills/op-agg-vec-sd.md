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
| `labels` | list | — | Inline `fields`: one display label per member. |
| `expand` | bool | — | One scalar column `<label>[<element label>]` per member instead of the array. |
<!-- feature: capability:crosstab -->
| `element` | string | — | Crosstab cell / margin only: one member by label, a scalar (its sd). |
<!-- /feature -->

Slot: `vector` or `fields`, no `field` (`vector-aggregates`). Default label `AGG_VEC_SD_<vector>`.<!-- feature: capability:weighting --> Weight-aware: √(M2 / (N* − 1)) per member, M2 = Σw·(x − mean)², N* = Σw under `kind: frequency` (the expanded rows), Kish n_eff = (Σw)²/Σw² under `probability`.<!-- /feature -->

## Inputs

Members: see `## Contract`; source columns only.

## Output

Rich `[]float64`, one SAMPLE standard deviation per member (n − 1<!-- feature: AGG_STDDEV -->; `AGG_STDDEV` is the population form unless `ddof: 1`<!-- /feature -->); a null member skips only that element. An element with N* ≤ 1 — one answering row, none, or all weight on one row — is NaN (`null`), never 0.

<!-- generated: reading-the-output -->

## Components

- `labels`, `element_n` (raw per member), `element_sum`, `element_mean` (Welford running mean), `element_m2`; weighted adds `element_sum_weights`, `element_sum_weights_sq`.

<!-- generated: contract -->

## Gotchas

- The array is refused (`PROCESSING_CONFIG`, reason `array_valued`) as a Sort key, a window, post-test or overlay input<!-- feature: capability:crosstab -->, a crosstab cell or margin without `element`<!-- /feature -->, or a chain stage feeding another — unless expanded (`vector-aggregates`).
- Expanded columns are ordinary nullable f64 columns; a `return` path names one quoted, `data[*]."<label>[<elem>]"`.
<!-- feature: capability:crosstab -->
- `element` cell: a record reaches an auxiliary margin only if its member reached a cell.
<!-- /feature -->

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: `vector-aggregates`, `op-agg-vec-mean`, `multivariate-design`<!-- feature: AGG_STDDEV -->, `op-agg-stddev`<!-- /feature -->
