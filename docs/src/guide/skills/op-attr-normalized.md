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

| Param | Accepted field types |
|---|---|
| `Field` | numeric (no `decimal128`): `u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`, `packed_bool`, `u4` |
| `Label` | required — new column name |

## Output

One `float64` per record in `[0, 1]`. Null source → `0` (not null).

## Reading the output

- `value`: Where the row's value sits between the smallest and largest value, on a 0-to-1 scale: (value - min) / (max - min). 0 is the minimum, 1 the maximum and 0.5 halfway between them, which is not the median.
  - Caveat: One extreme value sets the minimum or maximum and squeezes every other row into a narrow part of the scale; ATTR_PERCENTILE is not affected that way.
  - Caveat: A row with a missing value reads 0, the same as the minimum, and every row reads 0 when all values are equal (max = min).

## Gotchas

- Not weightable (a min-max rescale has no weighted meaning): `Options.DefaultWeight` is skipped; an explicit slot or request weight is `PROCESSING_CONFIG`.
- Two-pass: pre-pass tracks min/max across filter-passing rows; pass 2 emits per row.
- Constant field (`max == min`) → `0` per row.
- Outlier-sensitive — one extreme value compresses the rest of the range.
- `decimal128` rejected.
- Frequently used as a feature input for a downstream formula or external ML — but no in-slot chaining; stage via Compose / ProcessChain.
- `set_*` rejected at build time with `PROCESSING_CONFIG` — a bitmask has no value to standardise.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`attribute-composition`](attribute-composition.md), [`op-attr-zscore`](op-attr-zscore.md), [`op-attr-percentile`](op-attr-percentile.md)
