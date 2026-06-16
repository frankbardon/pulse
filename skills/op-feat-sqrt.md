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

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

None. `Field` (required, numeric) — `params` block is unused.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`. `decimal128` accepted via f64 approximation. (no categorical / `packed_bool`) |

## Output

One `f64` column written to `Label` (default `SQRT_<field>`). Formula `sqrt(x)`.

## Gotchas

- Negative inputs (`x < 0`) emit `null` — sqrt of negative is undefined for real-valued features. No error.
- Null inputs propagate to null outputs.
- Gentler skew compression than `FEAT_LOG` — prefer for moderate-skew counts; pair with `op-feat-log` when comparing transforms.
- Streamable per-row.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-log`, `op-feat-poly`
