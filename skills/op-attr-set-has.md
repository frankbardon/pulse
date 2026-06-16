---
name: op-attr-set-has
description: Per-row 0/1 — whether the configured label's bit is set on a set field.
kind: operator
category: ATTR
operator: ATTR_SET_HAS
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, streaming-friendly]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `label` | string | (required) | Dictionary label whose bit position is checked per row. Resolved against the set field's inline dictionary at schema time. |

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `set_u8`, `set_u16`, `set_u32`, `set_u64` |
| `Label` (the new column name) | required |

## Output

One `packed_bool` per record — `1` if the named label's bit is set, `0` otherwise. Null source → null output.

## Gotchas

- Row-local one-pass — streams cleanly.
- Unknown `label` (not in the field's dictionary) → `PROCESSING_CONFIG`.
- Frequently fanned out — one slot per label of interest — to build per-label binary cohorts. Multi-label tests are cheaper via `FILTER_SET_*` or a single `ATTR_FORMULA` using `has_any` / `has_all`.
- Coerces to `f64` (1.0 / 0.0) when read by downstream numeric ops.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `attribute-composition`, `cohort-schema-design`, `op-attr-set-popcount`
