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

<!-- generated: use-when -->

## Params

None.

## Inputs

`field` — admitted types: see `## Contract`.

- `label` — derived field name; default `<TYPE>_<field>`

## Output

One whole number per record (an `f64` column, `emits_type`), 0..set width **inclusive** — 0..256 at `set_u256`. Empty mask → `0` (valid). Null source → `null` (counted in `n_null`), never the empty mask's `0`.

<!-- generated: contract -->

## Gotchas

- Row-local one-pass — streams cleanly; merges (shards, parallel decode, chain stages).
- Filters run before attributes, so none reads this label: to keep "respondents who selected ≥ 3", filter on `popcount(issuers) >= 3`<!-- feature: FILTER_EXPRESSION --> with `FILTER_EXPRESSION`<!-- /feature -->.
- A fully-selected `set_u256` gives 256, one past a `u8`'s 255 — a `materialize` cast to an integer needs `u16`, not the rung's byte width.

## See

- `pulse_examples_search tags=[cardinality-analysis]`
- Skills: `attribute-composition`, `cohort-schema-design`, `op-attr-set-has`
