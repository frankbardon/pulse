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

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `u4` |
| `Label` | required — new column name |

## Output

One `float64` per record in `(0, 100]` — `rank / n * 100` within the filter-passing value set (smallest = `100/n`). Null source → `0` (not null).

## Reading the output

- `value`: The row's position among the sorted values, as a percentage: rank / n * 100, where the smallest value has rank 1 and n counts the rows with a value. The largest value reads 100 and the smallest 100 / n, so for a value no other row shares it is the share of rows at or below this one; tied values break that (see below).
  - Caveat: A row with a missing value reads 0, which no row with a value can: treat 0 as missing.
  - Caveat: Other tools define it differently (smallest at 0, or ties at their midpoint), so the same row can read a few points apart elsewhere.

## Gotchas

- Never weighted (a weighted rank cannot reproduce the distinct unweighted ranks): any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED` with `details.reason`; set `"weight": null` on the slot to run it unweighted.
- **NOT streamable** — pre-pass sorts the full filter-passing field; cohort-sized memory peak. Predict surfaces this under `streamable_reasons`.
- Ties do NOT share a percentile: each tied row gets its own rank, in arbitrary order.
- `decimal128` rejected.
- Under shard archives the sort runs per-shard along the `Mergeable` path; pass 2 emits global ranks once shards merge.
- `set_*` rejected at build time with `PROCESSING_CONFIG` — a bitmask has no value to standardise.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: [`attribute-composition`](attribute-composition.md), [`op-attr-normalized`](op-attr-normalized.md), [`op-agg-percentile`](op-agg-percentile.md)
