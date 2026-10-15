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

<!-- generated: use-when -->

## Params

None. Weight (both kinds, same scores; `"weight": null` opts out): weighted mean and population sd √(Σw(x − μ)²/Σw); a zero / invalid-weight row adds no mass but is still scored.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |
| `Label` | required — new column name |

## Output

One `float64` per record — `(value − pop_mean) / pop_stddev`. Null source → `0` (not null).

<!-- generated: reading-the-output -->

## Gotchas

- Two-pass: pre-pass computes mean/stddev over filter-passing rows, then pass 2 emits per row. Orchestrator handles transparently — no full buffering unless paired with a buffered op downstream.
- Zero stddev (constant field) → `0` per row.
- Two-pass attributes are not mergeable: shard archives and parallel decode take the sequential shard iterator, walking every shard for both passes — global standardization.
- `set_*` and categorical rejected at build time with `PULSE_FIELD_TYPE_UNSUPPORTED` — a bitmask or dictionary index has no value to standardise.

## See

- `pulse_examples_search tags=[outlier-detection]`
- Skills: `attribute-composition`, `op-attr-tscore`, `op-agg-zscore`
