---
name: op-feat-log
description: Per-row log1p(x) of a numeric field into one derived field. Standard skew-tamer.
kind: operator
category: FEAT
operator: FEAT_LOG
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

One derived field at `label` (default `LOG_<field>`). Formula `log1p(x) = ln(1 + x)` — the +1 shift keeps `x=0 -> 0` instead of `-inf`. Bit-identical to the formula attribute's `log1p(x)` on every machine (`log(x + 1)` is not: it rounds `1 + x` first).

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Inputs with `x <= -1` (where `1 + x <= 0`) produce a `null`, not an error — the rest of the cohort keeps going.
- Null inputs propagate to null outputs.
- Streamable per-row — pairs cleanly with online aggregators downstream.
- Runs before filters: a range filter on the output references `LOG_<field>` (default label).

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-sqrt`, `op-feat-poly`
