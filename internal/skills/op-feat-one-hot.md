---
name: op-feat-one-hot
description: One-hot encode a categorical field as one f64 column per dictionary entry.
kind: operator
category: FEAT
operator: FEAT_ONE_HOT
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, pre-filter, feature-pipeline]
---

Feature operators emit derived columns; no `Response.Components`.

## Params

None. `Field` (required, categorical) — `params` block is unused.

## Inputs

`Field` — `categorical_u8`, `categorical_u16`, `categorical_u32` (must carry a dictionary).

## Output

ONE column per dictionary entry, named `<prefix>_<category>`, prefix = `Label` (default `<field>`). Each row holds `1.0` in its category's column, `0.0` elsewhere. The column set comes from the SCHEMA dictionary at construction, so predict reports the full post-feature schema without scanning records.

## Gotchas

- Non-categorical or dictionary-less `Field` → `PROCESSING_CONFIG`.
- Whitespace in labels normalises to `_` for column-name safety (`"New York"` -> `<prefix>_New_York`); other punctuation is the caller's problem.
- Null categories emit ALL ZERO across the one-hot block (mirrors `Compute`). No implicit "unknown" column.
- A dictionary category absent from the records still gets an all-zero column, which keeps downstream layouts stable.
- Streamable per-row.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-frequency-encode`, `op-feat-target-encode`
