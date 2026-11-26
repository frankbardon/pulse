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

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `label` | string | (required) | Dictionary label whose bit position is checked per row. Resolved against the set field's inline dictionary at schema time. |

## Inputs

`field` — admitted types: see `## Contract`.

- `label` (slot) — derived field name, default `ATTR_SET_HAS_<field>`; `params.label` is the dictionary label

## Output

One `packed_bool` per record — `1` if the named label's bit is set, `0` otherwise. Null source → `0` (not null): a membership flag, so its mean is the share of the whole base.

<!-- generated: contract -->

## Gotchas

- Row-local one-pass — streams cleanly; merges (shards, parallel decode, chain stages).
- `params.label` not in the field's dictionary → `PROCESSING_CONFIG` at run time; predict passes it.
- Frequently fanned out — one slot per label of interest — to build per-label binary cohorts. Multi-label tests are cheaper as one set filter<!-- feature: ATTR_FORMULA --> or one `ATTR_FORMULA` using `has_any` / `has_all`<!-- /feature -->.
- Coerces to `f64` (1.0 / 0.0) when read by downstream numeric ops.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `attribute-composition`, `cohort-schema-design`, `op-attr-set-popcount`
