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
| `labels` | list | — | Inline `fields`: one display label per member. |
| `expand` | bool | — | One scalar column `<label>[<element label>]` per member instead of the array. |
<!-- feature: capability:crosstab -->
| `element` | string | — | Crosstab cell / margin only: one member by label, a scalar (its total). |
<!-- /feature -->

Slot: `vector` or `fields`, no `field` (`vector-aggregates`). Default label `AGG_VEC_SUM_<vector>`.<!-- feature: capability:weighting --> Weight-aware: Σw·x per member.<!-- /feature -->

## Inputs

Members: see `## Contract`; source columns only.

## Output

Rich `[]float64`, one total per member; a null member skips only that element. An element no row answered totals 0.

## Components

- `labels`, `element_n` (raw per member), `element_sum`; weighted adds `element_sum_weights`.

<!-- generated: contract -->

## Gotchas

- The array is refused (`PROCESSING_CONFIG`, reason `array_valued`) as a Sort key, a window, post-test or overlay input<!-- feature: capability:crosstab -->, a crosstab cell or margin without `element`<!-- /feature -->, or a chain stage feeding another — unless expanded (`vector-aggregates`).
- Expanded columns are ordinary nullable f64 columns; a `return` path names one quoted, `data[*]."<label>[<elem>]"`.
<!-- feature: capability:crosstab -->
- `element` cell: a record reaches an auxiliary margin only if its member reached a cell.
<!-- /feature -->

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: `vector-aggregates`, `op-agg-vec-mean`, `op-agg-vec-sd`, `multivariate-design`
