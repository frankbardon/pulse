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

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — required — new column name

## Output

One `float64` per record — `(value − pop_mean) / pop_stddev`. Null source → `null` (counted in `n_null`), left out of the mean / sd.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Two-pass: pre-pass computes mean/stddev over filter-passing rows, then pass 2 emits per row. Orchestrator handles transparently — no full buffering unless paired with a buffered op downstream.
- Zero stddev (constant field, or one non-null row) → `null` per row (0/0), counted in `n_null`.
- Two-pass attributes are not mergeable: shard archives and parallel decode take the sequential shard iterator, walking every shard for both passes — global standardization.

## See

- `pulse_examples_search tags=[outlier-detection]`
- Skills: `attribute-composition`, `op-attr-tscore`, `op-agg-zscore`
