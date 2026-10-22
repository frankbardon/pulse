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

| Name | Type | Default | Description |
|---|---|---|---|
| `Values` | `[2]string` | (required) | `[low, high]` — both parsed via `strconv.ParseFloat`. Exactly two entries; any other count → `PROCESSING_CONFIG`. |

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

Row-level predicate. Pass when `low ≤ value ≤ high`; drop otherwise. No emitted column.

<!-- generated: contract -->

## Gotchas

- Inclusive bounds on both ends. <!-- feature: FILTER_EXPRESSION -->Open intervals require `FILTER_EXPRESSION`.<!-- /feature -->
- Null rows fail the predicate (dropped).
- `date` interpreted as the numeric day-since-epoch — pass numeric strings, not formatted dates.

## See

- `pulse_examples_search tags=[cohort-analysis]`
- Skills: `aggregation-design`, `response-components`, `op-filter-expression`
