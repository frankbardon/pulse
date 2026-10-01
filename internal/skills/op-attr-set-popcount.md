---
name: op-attr-set-popcount
description: Per-row popcount of a set field — number of selected labels.
kind: operator
category: ATTR
operator: ATTR_SET_POPCOUNT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, cardinality-analysis, streaming-friendly]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Params

None.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | any set rung — `set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` |
| `Label` | required — new column name |

## Output

One integer per record, 0..set width **inclusive** — 0..256 at `set_u256`, hence `emits_type` `u16`, not `u8`. Empty mask → `0` (valid, distinct from null). Null source → null output.

## Gotchas

- Row-local one-pass — streams cleanly.
- Use as a downstream filterable signal ("respondents who selected ≥ 3 issues") — pair with `FILTER_GTE` after this attribute, or precompute as `FEAT` if you need it before the filterers slot.
- For the SUM of popcounts across rows use `AGG_SET_CARDINALITY_SUM`; for the average use `AGG_SET_CARDINALITY_AVG`.
- For "did this row select label X" use `ATTR_SET_HAS`.
- A fully-selected `set_u256` gives 256, one past a `u8`'s 255 — size any destination column from `emits_type`, not from the rung's byte width.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `attribute-composition`, `cohort-schema-design`, `op-attr-set-has`
