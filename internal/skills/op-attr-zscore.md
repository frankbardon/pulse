---
name: op-attr-zscore
description: Per-row standardized z-score column — (value − mean) / stddev via two-pass Welford.
kind: operator
category: ATTR
operator: ATTR_ZSCORE
type: reference
applies_to: process, compose, predict
examples_tags: [outlier-detection, distribution-shape, buffered-pipeline]
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

One `float64` per record — `(value − pop_mean) / pop_stddev`. Null source → `0` (not null).

## Gotchas

- No weighted form yet: any row weight in force on the slot (request, slot or `Options.DefaultWeight`) is `PULSE_WEIGHT_UNSUPPORTED`; set `"weight": null` on the slot to run it unweighted.
- Two-pass: pre-pass computes mean/stddev over filter-passing rows, then pass 2 emits per row. Orchestrator handles transparently — no full buffering unless paired with a buffered op downstream.
- Zero stddev (constant field) → `0` per row.
- `decimal128` rejected.
- Under sharded cohorts the two-pass runs per-shard along the `Mergeable` path — produces global standardization.
- `set_*` rejected at build time with `PROCESSING_CONFIG` — a bitmask has no value to standardise.

## See

- `pulse_examples_search tags=[outlier-detection]`
- Skills: `attribute-composition`, `op-attr-tscore`, `op-agg-zscore`
