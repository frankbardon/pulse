```yaml
name: op-agg-count
description: Count records that pass the active filter, optionally per group.
kind: operator
category: AGG
operator: AGG_COUNT
type: reference
applies_to: process, compose, predict
examples_tags: [streaming-friendly, cohort-analysis]
```

## Use when

Number of rows that have a value in a field, over all rows or per group.

Questions it answers:

- How many responses did each region return?
- How many orders were placed through each channel?

Use something else:

- `AGG_NULL_COUNT` when you want the rows where the field is empty.
- `AGG_DISTINCT_COUNT` when you want how many different values appear, not how many rows.

## Params

Weighted: Σw over value-present rows (a float).

## Inputs

`field` — any field type (see `## Contract`); counts non-null rows.

## Output

Scalar `int64`. Per-group when wired under a grouper; otherwise one row across the cohort.

## Contract

- Types: any type.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; components `mergeable`.
- Components: `n`, `n_null`; optional `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Counts non-null inputs only.
- `field` is required in every slot, a crosstab `cell` or `margin_aggregations` entry included (it once answered 0 in every cell): none → `SERVICE_VALIDATION`, at predict and at run time.
- Weighted (Σw, and the `sum_weights` / `n_eff` floor): a binned sum, the same bits on every worker count and shard split.
- Over `decimal128` the count is a JSON integer (`4`), never a decimal string.
- Never a smart default: an omitted `type` never infers `AGG_COUNT` — name it explicitly.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
