---
name: op-filter-range
description: Keep records whose numeric field value falls within [low, high] inclusive.
kind: operator
category: FILTER
operator: FILTER_RANGE
type: reference
applies_to: process, compose, predict
examples_tags: [cohort-analysis, streaming-friendly]
---

<!-- generated: use-when -->

## Params

Wire: `values` is a top-level filterer key (beside `field`), not under `params`.

| Name | Type | Default | Description |
|---|---|---|---|
| `values` | array | (required) | `[low, high]`, number strings. Any other count, or a bound that is not a number → `PROCESSING_CONFIG`, at predict and at run time. |

## Inputs

`field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

Row-level predicate. Pass when `low ≤ value ≤ high`; drop otherwise. No emitted column.

<!-- generated: contract -->

## Gotchas

- Inclusive bounds on both ends; there is no open bound, so never use a sentinel like `255`. <!-- feature: FILTER_EXPRESSION -->One-sided ("at least 40"): `{"type": "FILTER_EXPRESSION", "expression": "age >= 40"}`.<!-- /feature -->
- Null rows fail the predicate (dropped).
- `date` interpreted as the numeric day-since-epoch — pass numeric strings, not formatted dates.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-expression`
