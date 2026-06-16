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

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `nullable_*` |
| `Label` | required — new column name |

## Output

One `float64` per record — `50 + 10 * zscore`. Null source → null output.

## Gotchas

- Two-pass: shares the Welford pre-pass with `ATTR_ZSCORE`. Reading-friendly scale for survey / education contexts (mean 50, sd 10, no negatives in the typical range).
- Zero stddev → `NaN`.
- `decimal128` rejected.
- Not a percentile — same shape as the underlying distribution. For rank-style scoring use `ATTR_PERCENTILE` or `ATTR_NORMALIZED`.

## See

- `pulse_examples_search tags=[distribution-shape]`
- Skills: `attribute-composition`, `op-attr-zscore`, `op-attr-normalized`
