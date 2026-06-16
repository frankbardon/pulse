---
name: op-agg-ratio
description: Emits sum(numerator_field) / sum(denominator_field). The Aggregation's own Field is ignored.
kind: operator
category: AGG
operator: AGG_RATIO
type: reference
applies_to: process, compose, predict
examples_tags: [proportion-analysis, streaming-friendly]
---

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `numerator_field` | string | (required) | Schema field summed as the numerator. |
| `denominator_field` | string | (required) | Schema field summed as the denominator. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | ignored |
| `numerator_field` / `denominator_field` | any cohort field; coerced to float64 |

## Output

Scalar `float64` — `sum(num) / sum(den)`. NaN when denominator sum == 0.

## Components

Universal floor `{n, n_null}` plus operator-specific:

| Key | Type | Notes |
|---|---|---|
| `numerator` | float64 | Running num sum |
| `denominator` | float64 | Running den sum |
| `ratio` | float64 | Resolved ratio (NaN if den==0) |

- Mergeability: `Mergeable` (two independent sums)
- Streaming: per-chunk

## Gotchas

- Aggregation's `Field` is IGNORED — inputs come from Params.
- Denominator-zero returns NaN, not Inf or error.
- `n` counts contributing rows, not distinct den-non-zero rows.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: `aggregation-design`, `response-components`
