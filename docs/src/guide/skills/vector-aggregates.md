```yaml
name: vector-aggregates
description: The array-valued vector aggregators — the vector-or-fields slot and labels, per-element missing data, expand into one scalar column per member and where those columns can go, where an unexpanded array is refused, and element for one member in a crosstab cell or margin. Topical design; per-operator formulas in the atomic op-agg-vec-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [vector aggregator, array_valued, expand, element, labels, element_n, per-element missing data]
```

# Vector aggregates: arrays, `expand`, `element`

A vector aggregator (`AGG_VEC_MEAN`) (`AGG_VEC_SUM`) (`AGG_VEC_SD`) returns one figure per member as a rich `[]float64`, per group row when grouped. Which operator: [`multivariate-design`](multivariate-design.md); the formula: its atomic skill.

## Slot

- Exactly one of `vector` (a `vectors` entry, its labels kept) or inline `fields`; no `field`. Default label `<TYPE>_<vector>`.
- `labels` (inline `fields` only) — one display label per member; absent: the member names. Element labels must be distinct.
- Per-element missing data: a null member skips only that element. A row is present (the floor's `n`) when ANY member answered; `element_n` is the raw count per member.
- Components are per-member arrays (`element_*`; the sd's moments Chan-combine per merge block), bit-identical for any worker count or shard split.

## `expand`

`expand: true` emits one scalar column per member, `<label>[<element label>]`, instead of the array (grouped: per row); the slot's own label is then absent. Predict lists the columns (`aggregations[i].columns`).

Expanded columns are ordinary nullable `f64` columns: `sort`, a window (`field` / `order_by` / `partition_by`), a post-test `field`, a series overlay's `params.field` and a later chain stage (as `f64` input fields; NaN → null) read them. A `return` path names one quoted, `data[*]."<label>[<elem>]"`, or all of them with `data[*].<label>*`.

## Where an array is refused

An unexpanded array is `PROCESSING_CONFIG`, reason `array_valued`, as:

- a crosstab cell or margin aggregation without `element`;
- a Sort key, a window input or a post-test input;
- the value an overlay scores (request, Compose or whole-chain: `params.field` where the kind honours it, else `aggregations[0]` — an unscored array beside it runs);
- a chain stage feeding another stage.

## `element` (crosstab)

`element` names one member's display label in a crosstab `cell` or `margin_aggregations` entry: the slot reads that member alone and answers a scalar (its mean, total or sd). Refused with `expand`, outside a crosstab, or for an unknown or shared label (`SERVICE_VALIDATION`, reason `element_*`).

Floor and components are the member's (one-entry arrays). **A record reaches an auxiliary margin only if its member reached a cell.** The mean and sum fuse like their scalar twins; the sd's margin is recomputed over raw rows, so that crosstab runs buffered. Margins: [`crosstab-margin-aggregations`](crosstab-margin-aggregations.md).

## See

- [`multivariate-design`](multivariate-design.md), [`aggregation-design`](aggregation-design.md), [`crosstab-margin-aggregations`](crosstab-margin-aggregations.md), [`response-shaping`](response-shaping.md)
