---
name: op-attr-percentile
description: Per-row percentile rank column against the post-filter value set; requires sorting.
kind: operator
category: ATTR
operator: ATTR_PERCENTILE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, distribution-shape, buffered-pipeline]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `nullable_*` |
| `Label` | required — new column name |

## Output

One `float64` per record in `(0, 100]` — the row's percentile within the filter-passing value set. Null source → null output.

## Gotchas

- **NOT streamable** — pre-pass sorts the full filter-passing field; cohort-sized memory peak. Predict surfaces this under `streamable_reasons`.
- Tie handling: ranks share a percentile (no fractional split).
- For streaming-friendly relative position use `ATTR_NORMALIZED` (min-max) as a proxy.
- `decimal128` rejected.
- Under shard archives the sort runs per-shard along the `Mergeable` path; pass 2 emits global ranks once shards merge.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `attribute-composition`, `op-attr-normalized`, `op-agg-percentile`
