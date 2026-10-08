---
name: op-agg-mode
description: Most-frequent value of the field (ties broken by the smallest value).
kind: operator
category: AGG
operator: AGG_MODE
type: reference
applies_to: process, compose, predict
examples_tags: [cardinality-analysis, cross-tabulation]
---

<!-- generated: use-when -->

## Params

Weight-aware (`"weight": null` opts out): the value with the largest Σw; `count` becomes that Σw (a float). Invalid weights excluded (`PULSE_WEIGHT_INVALID_ROWS`).

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any field type EXCEPT `set_*` |

## Output

Scalar `float64` — the modal value (categorical: its dictionary index). Per-group under a grouper. ProcessChain admits it: a later stage reads that index as a number, not a label.

## Components

<!-- feature: capability:weighting -->Weighted adds floor `sum_weights`, `n_eff` (`probability`), `n_weight_invalid`.<!-- /feature -->

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `value` | any | Most-frequent value |
| `count` | int | Row count of the modal value (weighted: Σw) |
| `distinct_count` | int | Distinct values observed |
| `tie_count` | int | Values tied at the max count |

- Mergeability: `Partial` — map allocation
- Streaming: per-value counts merged at flush

## Gotchas

- Ties go to the SMALLEST value (or index), not the first seen; `tie_count > 1` flags a tie.
- High cardinality costs memory; pre-filter.
- `set_*` → `PROCESSING_CONFIG` (no modal scalar).

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `aggregation-design`, `response-components`
