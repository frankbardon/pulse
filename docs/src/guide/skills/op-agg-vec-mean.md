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

- `labels` — inline `fields` only: one display label per member (default the member names).

Slot: exactly one of `vector` (a `vectors` entry, its labels kept) / `fields`; no `field`. Default label `AGG_VEC_MEAN_<vector>`. Weight-aware: Σw·x / Σw per member.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

Rich `[]float64`, one mean per member; per-element missing data (a null member skips only that element). An element no row answered is NaN (`null`).

## Components

Floor `{n, n_null}` — a row is present when ANY member answered (weighted adds `sum_weights`, `n_eff`, `n_weight_invalid`). Plus `labels`, `element_n` (raw per member), `element_sum`, `element_sum_weights` (weighted). `Mergeable`; streams; bit-identical for any worker count.

## Gotchas

- Array-valued: refused (`PROCESSING_CONFIG`) as a crosstab cell, a Sort key, the first aggregation under overlays, and a chain stage feeding another.

## See

- `pulse_examples_search tags=[multivariate, streaming-friendly]`
- Skills: [`op-agg-vec-sum`](op-agg-vec-sum.md), [`op-attr-scale-score`](op-attr-scale-score.md), [`multivariate-design`](multivariate-design.md)
