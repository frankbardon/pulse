---
name: op-agg-count
description: Count records that pass the active filter, optionally per group.
kind: operator
category: AGG
operator: AGG_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, cohort-analysis]
---

<!-- generated: use-when -->

## Params

Weighted: Σw over value-present rows (a float).

## Inputs

- `Field` — any cohort field type (numeric, categorical, date, packed_bool, set_*, decimal128)

`AGG_COUNT` counts non-null rows.

## Output

Scalar `int64`. Per-group when wired under a grouper; otherwise one row across the cohort.

<!-- generated: contract -->

## Gotchas

- Counts non-null inputs only.
- Weighted (Σw, and the `sum_weights` / `n_eff` floor): a binned sum, the same bits on every worker count and shard split.
- Over `decimal128` the count is a JSON integer (`4`), never a decimal string.
- Never a smart default: an omitted `Type` never infers `AGG_COUNT` — name it explicitly.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: `aggregation-design`, `response-components`
