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
| `Field` | `set_u8`, `set_u16`, `set_u32`, `set_u64` |
| `Label` | required — new column name |

## Output

One small integer per record (encoded as `u8`, range 0..set width). Empty mask → `0` (valid, distinct from null). Null source → null output.

## Gotchas

- Row-local one-pass — streams cleanly.
- Use as a downstream filterable signal ("respondents who selected ≥ 3 issues") — pair with `FILTER_GTE` after this attribute, or precompute as `FEAT` if you need it before the filterers slot.
- For the SUM of popcounts across rows use `AGG_SET_CARDINALITY_SUM`; for the average use `AGG_SET_CARDINALITY_AVG`.
- For "did this row select label X" use `ATTR_SET_HAS`.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `attribute-composition`, `cohort-schema-design`, `op-attr-set-has`
