---
name: op-feat-sqrt
description: Per-row sqrt(x) of a numeric field into one derived field.
kind: operator
category: FEAT
operator: FEAT_SQRT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, distribution-shape, pre-filter, streaming-friendly]
---

Feature operators write derived fields before filters run; no `Response.Components`.

<!-- generated: use-when -->

## Params

None; `params` is unused.

## Inputs

`field` (required) — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

## Output

One derived field at `label` (default `SQRT_<field>`). Formula `sqrt(x)`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Negative inputs (`x < 0`) emit `null` — sqrt of negative is undefined for real-valued features. No error.
- Null inputs propagate to null outputs.
- Gentler skew compression than a log — prefer it for moderate-skew counts.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-log`, `op-feat-poly`
