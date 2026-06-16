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

Feature operators emit row-level/derived columns; they do not produce `Response.Components`.

## Params

None. `Field` (required, categorical) — `params` block is unused.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | `categorical_u8`, `categorical_u16`, `categorical_u32` (must carry a dictionary) |

## Output

ONE column per dictionary entry, named `<prefix>_<category>` where prefix is `Label` (default `<field>`). Each row holds `1.0` in the column matching its category, `0.0` in every other. The column set is materialised from the SCHEMA dictionary at construction — predict reports the full post-feature schema without scanning records.

## Gotchas

- Field must be categorical with a dictionary — otherwise `PROCESSING_CONFIG` ("must be categorical with a dictionary").
- Whitespace in category labels is normalised to `_` for column-name safety (`"New York"` -> `<prefix>_New_York`); other punctuation is left to the caller.
- Null categories emit ALL ZERO across the one-hot block (mirrors `Compute`). No implicit "unknown" column.
- Categories not present in records but present in the dictionary still get an all-zero column — useful for stable downstream layouts.
- Streamable per-row.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `feature-engineering`, `op-feat-frequency-encode`, `op-feat-target-encode`
