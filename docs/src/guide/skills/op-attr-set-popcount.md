```yaml
name: op-attr-set-popcount
description: Per-row popcount of a set field — number of selected labels.
kind: operator
category: ATTR
operator: ATTR_SET_POPCOUNT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, cardinality-analysis, streaming-friendly]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row the number of options its multi-select field has selected.

Questions it answers:

- How many reasons did each respondent tick?
- Which customers selected three or more interests?

Use something else:

- `AGG_SET_CARDINALITY_AVG` when you want the average number of selections per group.
- `ATTR_SET_HAS` when you want whether one particular option was chosen.

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`.

- `Label` — required — new column name

## Output

One integer per record, 0..set width **inclusive** — 0..256 at `set_u256`, hence `emits_type` `u16`, not `u8`. Empty mask → `0` (valid). Null source → `null` (counted in `n_null`), never the empty mask's `0`.

## Contract

- Types (`field`): `set_*`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Row-local one-pass — streams cleanly; merges (shards, parallel decode, chain stages).
- Use as a downstream filterable signal ("respondents who selected ≥ 3 issues") — pair with `FILTER_GTE` after this attribute, or precompute as `FEAT` if you need it before the filterers slot.
- A fully-selected `set_u256` gives 256, one past a `u8`'s 255 — size any destination column from `emits_type`, not from the rung's byte width.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: [`attribute-composition`](attribute-composition.md), [`cohort-schema-design`](cohort-schema-design.md), [`op-attr-set-has`](op-attr-set-has.md)
