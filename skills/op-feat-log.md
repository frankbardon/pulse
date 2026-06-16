---
name: op-feat-log
description: Per-row log1p(x) of a numeric field; emits one f64 column. Standard skew-tamer.
kind: operator
category: FEAT
operator: FEAT_LOG
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, distribution-shape, pre-filter, streaming-friendly]
---

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

None. `Field` (required, numeric) — `params` block is unused.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `decimal128` accepted via f64 approximation. (no categorical / `packed_bool`) |

## Output

One `f64` column written to `Label` (default `LOG_<field>`). Formula `log1p(x) = ln(1 + x)` — the +1 shift keeps `x=0 -> 0` instead of `-inf`.

## Gotchas

- Inputs with `x <= -1` (where `1 + x <= 0`) produce a `null`, not an error — the rest of the cohort keeps going.
- Null inputs propagate to null outputs.
- Streamable per-row — pairs cleanly with online aggregators downstream.
- Pre-filter slot: a `FILTER_RANGE` on the log column references `LOG_<field>` (default label).

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-sqrt`, `op-feat-poly`
