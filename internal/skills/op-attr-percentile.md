---
name: op-attr-percentile
description: Per-row percentile rank against the post-filter value set; requires sorting.
kind: operator
category: ATTR
operator: ATTR_PERCENTILE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, distribution-shape, buffered-pipeline]
---

<!-- generated: use-when -->

## Params

None.

## Inputs

`field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `label` — derived field name; default `<TYPE>_<field>`

## Output

One `float64` per record in `(0, 100]` — `rank / n * 100` within the filter-passing value set (smallest = `100/n`). Null source → `null` (counted in `n_null`), not ranked or counted in `n`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- **NOT streamable** — pre-pass sorts the full filter-passing field; cohort-sized memory peak. Predict surfaces this under `streamable_reasons`.
- Ties do NOT share a percentile: each tied row gets its own rank, in scan order (the earlier record ranks lower), so a rerun on the same cohort reads the same percentiles.
- Under shard archives ranks stay cohort-wide, never per shard: a buffered attribute is not mergeable, so the run takes the serial path, one scan across every shard.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `attribute-composition`, `op-attr-normalized`, `op-agg-percentile`
