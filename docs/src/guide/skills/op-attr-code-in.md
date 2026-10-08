```yaml
name: op-attr-code-in
description: Per-row 0/1 — whether a field's value is one of a list of codes; its mean is the share of the whole base.
kind: operator
category: ATTR
operator: ATTR_CODE_IN
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, proportion-analysis, streaming-friendly]
```

## Use when

Adds a 1 / 0 column saying whether each row's code is one of a listed set, keeping every row in the base.

Questions it answers:

- What share of all respondents gave a top-two-box answer (codes 4 or 5)?
- Which orders carry one of the priority status codes, as a column to average by region?

Use something else:

- `FILTER_INCLUDE` when you want to keep only the rows with those codes.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `codes` | array | (required) | Non-empty; strings or integers (`4` ≡ `"4"`); a fraction → `PROCESSING_CONFIG` |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `categorical_u8/u16/u32` (by LABEL, not id); `u4`/`u8`/`u16`/`u32`/`u64` (by value) |
| `Label` (output name) | required; `target` → `PROCESSING_CONFIG` |

## Output

`packed_bool` per row: `1` if the value is a code, else `0`. Null → `0`.

## Gotchas

- The label's mean (`AGG_WEIGHTED_MEAN`) is the share of the WHOLE base (top-box %). `FILTER_INCLUDE` on the codes shrinks the base: every cell reads 100%.
- Nulls count as "no". To drop them, put `FILTER_NULL` (`values: ["is_not_null"]`) first.
- Integer field: a code not a whole number in range (`u4` 0..15, `u8` 0..255…) → `PROCESSING_CONFIG`.
- A categorical code absent from the dictionary matches nothing; predict warns `PULSE_ATTR_CODE_NOT_IN_DICTIONARY` (error under `--strict`).
- `Field` must be a cohort schema field: a column derived by an earlier attribute or feature → `PROCESSING_CONFIG` "unknown field".
- Row-local: streams; fuses on crosstabs.

## See

- `pulse_examples_search tags=[proportion-analysis, feature-engineering]`
- Skills: [`attribute-composition`](attribute-composition.md), [`op-attr-set-has`](op-attr-set-has.md), [`weighting`](weighting.md)
