---
name: op-attr-normalized
description: Per-row min-max normalized column — (value − min) / (max − min) ∈ [0, 1].
kind: operator
category: ATTR
operator: ATTR_NORMALIZED
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, buffered-pipeline]
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

One `float64` per record in `[0, 1]`. Null source → null output.

## Gotchas

- Two-pass: pre-pass tracks min/max across filter-passing rows; pass 2 emits per row.
- Constant field (`max == min`) → `NaN` per row.
- Outlier-sensitive — one extreme value compresses the rest of the range. For robust scaling prefer `ATTR_PERCENTILE` (rank-based) or `ATTR_ZSCORE` (centered).
- `decimal128` rejected.
- Frequently used as a feature input for downstream `ATTR_FORMULA` or external ML — but no in-slot chaining; stage via Compose / ProcessChain.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `attribute-composition`, `op-attr-zscore`, `op-attr-percentile`
