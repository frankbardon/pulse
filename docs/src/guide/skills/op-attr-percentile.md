```yaml
name: op-attr-percentile
description: Per-row percentile rank column against the post-filter value set; requires sorting.
kind: operator
category: ATTR
operator: ATTR_PERCENTILE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, distribution-shape, buffered-pipeline]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied.

Questions it answers:

- Where does each store's revenue rank among all stores, as a percentage?
- Which respondents are in the top tenth for spend?

Use something else:

- `AGG_PERCENTILE` when you want the value at a chosen percentile, such as the 90th.
- `GROUP_QUANTILE` when you want rows split into equal-sized bands.

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — required — new column name

## Output

One `float64` per record in `(0, 100]` — `rank / n * 100` within the filter-passing value set (smallest = `100/n`). Null source → `null` (counted in `n_null`), not ranked or counted in `n`.

## Reading the output

- `value`: The row's position among the sorted values, as a percentage: rank / n * 100, where the smallest value has rank 1 and n counts the rows with a value. The largest value reads 100 and the smallest 100 / n, so for a value no other row shares it is the share of rows at or below this one; tied values break that (see below).
  - Caveat: A row with a missing value gets no percentile (null) and is not counted in n.
  - Caveat: Other tools define it differently (smallest at 0, or ties at their midpoint), so the same row can read a few points apart elsewhere.

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: refused under any weight in force, an instance default included (`PULSE_WEIGHT_UNSUPPORTED`: a weighted percentile rank shares tied rows inclusively, which cannot reproduce the unweighted attribute's distinct ranks at unit weights); `weight: null` on the slot runs it unweighted.
- Streaming: buffered only. Use ATTR_NORMALIZED for a streaming-friendly rank proxy.

## Gotchas

- **NOT streamable** — pre-pass sorts the full filter-passing field; cohort-sized memory peak. Predict surfaces this under `streamable_reasons`.
- Ties do NOT share a percentile: each tied row gets its own rank, in scan order (the earlier record ranks lower), so a rerun on the same cohort reads the same percentiles.
- Under shard archives the sort runs per-shard along the `Mergeable` path; pass 2 emits global ranks once shards merge.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`attribute-composition`](attribute-composition.md), [`op-attr-normalized`](op-attr-normalized.md), [`op-agg-percentile`](op-agg-percentile.md)
