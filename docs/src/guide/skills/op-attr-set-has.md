```yaml
name: op-attr-set-has
description: Per-row 0/1 — whether the configured label's bit is set on a set field.
kind: operator
category: ATTR
operator: ATTR_SET_HAS
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, streaming-friendly]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds a 1 / 0 column saying whether each row's multi-select field includes one named option.

Questions it answers:

- Which respondents ticked 'price' among their reasons, as a column to cross with other answers?
- Which tickets carry the 'urgent' tag?

Use something else:

- `FILTER_SET_CONTAINS_ANY` when you want to keep only rows that include any of several options.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `label` | string | (required) | Dictionary label whose bit position is checked per row. Resolved against the set field's inline dictionary at schema time. |

## Inputs

`Field` — admitted types: see `## Contract`.

- `Label` (the new column name) — required

## Output

One `packed_bool` per record — `1` if the named label's bit is set, `0` otherwise. Null source → `0` (not null): a membership flag, so its mean is the share of the whole base.

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Row-local one-pass — streams cleanly; merges (shards, parallel decode, chain stages).
- Unknown `label` (not in the field's dictionary) → `PROCESSING_CONFIG`.
- Frequently fanned out — one slot per label of interest — to build per-label binary cohorts. Multi-label tests are cheaper as one set filter or one `ATTR_FORMULA` using `has_any` / `has_all`.
- Coerces to `f64` (1.0 / 0.0) when read by downstream numeric ops.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: [`attribute-composition`](attribute-composition.md), [`cohort-schema-design`](cohort-schema-design.md), [`op-attr-set-popcount`](op-attr-set-popcount.md)
