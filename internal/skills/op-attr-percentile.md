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
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `u4` |
| `Label` | required — new column name |

## Output

One `float64` per record in `(0, 100]` — `rank / n * 100` within the filter-passing value set (smallest = `100/n`). Null source → `0` (not null).

## Gotchas

- No weighted form yet: any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED`; set `"weight": null` on the slot to run it unweighted.
- **NOT streamable** — pre-pass sorts the full filter-passing field; cohort-sized memory peak. Predict surfaces this under `streamable_reasons`.
- Ties do NOT share a percentile: each tied row gets its own rank, in arbitrary order.
- `decimal128` rejected.
- Under shard archives the sort runs per-shard along the `Mergeable` path; pass 2 emits global ranks once shards merge.
- `set_*` rejected at build time with `PROCESSING_CONFIG` — a bitmask has no value to standardise.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `attribute-composition`, `op-attr-normalized`, `op-agg-percentile`
