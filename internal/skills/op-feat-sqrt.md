---
name: op-feat-sqrt
description: Per-row sqrt(x) of a numeric field; emits one f64 column.
kind: operator
category: FEAT
operator: FEAT_SQRT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, distribution-shape, pre-filter, streaming-friendly]
---

Feature operators emit derived columns; no `Response.Components`.

<!-- generated: use-when -->

## Params

None. `Field` (required, numeric) — `params` block is unused.

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

One `f64` column written to `Label` (default `SQRT_<field>`). Formula `sqrt(x)`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Negative inputs (`x < 0`) emit `null` — sqrt of negative is undefined for real-valued features. No error.
- Null inputs propagate to null outputs.
- Gentler skew compression than a log — prefer it for moderate-skew counts.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-log`, `op-feat-poly`
