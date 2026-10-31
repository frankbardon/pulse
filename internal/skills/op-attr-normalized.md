---
name: op-attr-normalized
description: Per-row min-max normalized column — (value − min) / (max − min) ∈ [0, 1].
kind: operator
category: ATTR
operator: ATTR_NORMALIZED
type: reference
applies_to: process, compose, predict
examples_tags: [feature-engineering, buffered-pipeline]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

None.

## Inputs

`Field` — admitted types: see `## Contract`; `decimal128` reads as its float64 echo, `packed_bool` as 0/1.

- `Label` — required — new column name

## Output

One `float64` per record in `[0, 1]`. Null source → `null` (counted in `n_null`), left out of min / max.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- A min-max rescale has no weighted meaning.
- Two-pass: pre-pass tracks min/max across filter-passing rows; pass 2 emits per row.
- Constant field (`max == min`, or one non-null row) → `null` per row (0/0), counted in `n_null`.
- Outlier-sensitive — one extreme value compresses the rest of the range.
- Feeds a downstream formula: a LATER formula attribute reads its label; materialise it (`derived-cohorts`) for a later request.

## See

- `pulse_examples_search tags=[feature-engineering]`
- Skills: `attribute-composition`, `op-attr-zscore`, `op-attr-percentile`
