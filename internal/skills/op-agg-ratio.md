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

`numerator_field`, `denominator_field` — required field names, any type, read through the numeric channel (a categorical contributes its dictionary code). An unknown name is `SERVICE_VALIDATION`, predict and runtime alike.

## Inputs

`Field` is IGNORED (manifest `ignores_field`) but still required on the wire; `accepts_types` refuses no type there.

## Output

Scalar `float64` — `sum(num) / sum(den)`. NaN when the denominator sum is 0 (not Inf, not an error).

## Components

Floor `{n, n_null}` plus:

| Key | Type | Notes |
|---|---|---|
| `numerator` | float64 | Running num sum |
| `denominator` | float64 | Running den sum |
| `ratio` | float64 | Resolved ratio (NaN if den==0) |

`Mergeable` (two independent sums); streams per chunk.

## Gotchas

- `n` counts contributing rows, not den-non-zero rows.

## See

- `pulse_examples_search tags=[proportion-analysis]`
- Skills: `aggregation-design`, `response-components`
