```yaml
name: op-attr-normalized
description: Per-row min-max normalized column — (value − min) / (max − min) ∈ [0, 1].
kind: operator
category: ATTR
operator: ATTR_NORMALIZED
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, buffered-pipeline]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Rescales a numeric field to 0 to 1 on every row: 0 is the smallest value, 1 the largest.

Questions it answers:

- How can fields on very different scales be put on one 0-to-1 scale before combining them?
- Where does each row's value sit between the lowest and highest seen?

Use something else:

- `ATTR_PERCENTILE` when the field has extreme values that would squeeze the rest of the range.

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — required — new column name

## Output

One `float64` per record in `[0, 1]`. Null source → `null` (counted in `n_null`), left out of min / max.

## Reading the output

- `value`: Where the row's value sits between the smallest and largest value, on a 0-to-1 scale: (value - min) / (max - min). 0 is the minimum, 1 the maximum and 0.5 halfway between them, which is not the median.
  - Caveat: One extreme value sets the minimum or maximum and squeezes every other row into a narrow part of the scale; ATTR_PERCENTILE is not affected that way.
  - Caveat: A row with a missing value gets no value (null) and is left out of the minimum and maximum; every row reads null when all values are equal (max = min: 0/0, undefined).

## Contract

- Types (`field`): `date`, `datetime`, `decimal128`, `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: no weighted form; an explicit weight is `PROCESSING_CONFIG`, an instance default is skipped.
- Streaming: streamable.

## Gotchas

- A min-max rescale has no weighted meaning.
- Two-pass: pre-pass tracks min/max across filter-passing rows; pass 2 emits per row.
- Constant field (`max == min`, or one non-null row) → `null` per row (0/0), counted in `n_null`.
- Outlier-sensitive — one extreme value compresses the rest of the range.
- Frequently used as a feature input for a downstream formula or external ML — but no in-slot chaining; stage via Compose / ProcessChain.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`attribute-composition`](attribute-composition.md), [`op-attr-zscore`](op-attr-zscore.md), [`op-attr-percentile`](op-attr-percentile.md)
