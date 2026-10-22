---
name: op-attr-tscore
description: Per-row T-score column — z-score rescaled to mean 50, stddev 10.
kind: operator
category: ATTR
operator: ATTR_TSCORE
type: reference
applies_to: process, compose, predict
examples_tags: [distribution-shape, buffered-pipeline]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

None. Weight (both kinds, same scores; `"weight": null` opts out): weighted mean and population sd √(Σw(x − μ)²/Σw); a zero / invalid-weight row adds no mass but is still scored.

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — required — new column name

## Output

One `float64` per record — `50 + 10 * zscore`. Null source → `null` (counted in `n_null`), left out of the mean / sd.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Two-pass: a Welford pre-pass computes the mean and sd. Reading-friendly scale for survey / education contexts (mean 50, sd 10, no negatives in the typical range).
- Zero stddev (constant field, or one non-null row) → `null` per row (0/0), counted in `n_null`.
- Not a percentile — same shape as the underlying distribution.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `attribute-composition`, `op-attr-zscore`, `op-attr-normalized`
