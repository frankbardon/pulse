---
name: op-attr-code-in
description: Per-row 0/1 — whether a field's value is one of a list of codes; its mean is the share of the whole base.
kind: operator
category: ATTR
operator: ATTR_CODE_IN
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, proportion-analysis, streaming-friendly]
---

<!-- generated: use-when -->

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

- The label's mean<!-- feature: AGG_WEIGHTED_MEAN --> (`AGG_WEIGHTED_MEAN`)<!-- /feature --> is the share of the WHOLE base (top-box %).<!-- feature: FILTER_INCLUDE --> `FILTER_INCLUDE` on the codes shrinks the base: every cell reads 100%.<!-- /feature -->
- Nulls count as "no".<!-- feature: FILTER_NULL --> To drop them, put `FILTER_NULL` (`values: ["is_not_null"]`) first.<!-- /feature -->
- Integer field: a code not a whole number in range (`u4` 0..15, `u8` 0..255…) → `PROCESSING_CONFIG`.
- A categorical code absent from the dictionary matches nothing; predict warns `PULSE_ATTR_CODE_NOT_IN_DICTIONARY` (error under `--strict`).
- Row-local: streams; fuses on crosstabs.

## See

- `pulse_examples_search tags=[proportion-analysis, feature-engineering]`
- Skills: `attribute-composition`, `op-attr-set-has`, `weighting`
