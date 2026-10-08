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

Weight: honours a resolved row weight (`weight` on the request or slot, or `Options.DefaultWeight`; `"weight": null` opts out) — weighted it is Σw over value-present rows (a float). Invalid weights (null, negative, NaN/Inf, fractional under `frequency`) are excluded and warned (`PULSE_WEIGHT_INVALID_ROWS`); zero contributes nothing.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any cohort field type (numeric, categorical, date, packed_bool, set_*, decimal128) |

`AGG_COUNT` counts non-null rows.

## Output

Scalar `int64`. Per-group when wired under a grouper; otherwise one row across the cohort.

## Components

Weighted slots add floor keys `sum_weights` (Σw), `n_eff` (Kish; `probability` only) and `n_weight_invalid`; absent ⇒ unweighted. `n`/`n_null` stay raw counts.

Floor only — no operator-specific keys. Universal `{n, n_null}` per response-components contract.

- Mergeability: `Mergeable`
- Streaming: per-chunk emission; orchestrator sums

## Gotchas

- Counts non-null inputs only.
- Never a smart default: an omitted `Type` never infers `AGG_COUNT` — name it explicitly.
- `decimal128` under any weight in force (default included) → `PULSE_WEIGHT_UNSUPPORTED`; `"weight": null` opts out.

## See

- `pulse_examples_search tags=[streaming-friendly]`
- Skills: [`aggregation-design`](aggregation-design.md), [`response-components`](response-components.md)
