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

<!-- generated: use-when -->

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `datetime`, `decimal128` (its float64 echo), `packed_bool` (0/1); a categorical (its dictionary index) or `set_*` field is refused `PULSE_FIELD_TYPE_UNSUPPORTED` by predict and the runtime |
| `Label` | required — new column name |

## Output

One `float64` per record in `[0, 1]`. Null source → `0` (not null).

<!-- generated: reading-the-output -->

## Gotchas

- Not weightable (a min-max rescale has no weighted meaning): `Options.DefaultWeight` is skipped; an explicit slot or request weight is `PROCESSING_CONFIG`.
- Two-pass: pre-pass tracks min/max across filter-passing rows; pass 2 emits per row.
- Constant field (`max == min`) → `0` per row.
- Outlier-sensitive — one extreme value compresses the rest of the range.
- Frequently used as a feature input for a downstream formula or external ML — but no in-slot chaining; stage via Compose / ProcessChain.
- `set_*` and categorical rejected at build time with `PULSE_FIELD_TYPE_UNSUPPORTED` — a bitmask or dictionary index has no value to standardise.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `attribute-composition`, `op-attr-zscore`, `op-attr-percentile`
