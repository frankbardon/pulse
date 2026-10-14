---
name: op-attr-pc-score
description: Per-row standardized score on one principal component of a vector of numeric members, with the PCA matrix operator's sign and order and optional varimax; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_PC_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, feature-engineering, streaming-friendly]
---

Row-level scalars; no `Response.Components`.

<!-- generated: use-when -->

## Params

- `component` — required, 1-based. `on` — `correlation` (default) or `covariance`. `missing` — `listwise` only.
- `rotation` — `none` (default) or `varimax` with `components` = k rotated within (component ≤ k ≤ p).
- `fit_on` — `"filtered"` (default) or a filterer array picking the FIT rows; every filtered row is scored.
- `fit` — a bound vector fit's name, scored against instead of fitting (XOR `fit_on`: `fit_and_fit_on`); its own weight basis governs. Matched BY LABEL to the members (`labels` — inline `fields` only); mismatch `PULSE_VECTOR_REFERENCE_MISMATCH`, unbound `PULSE_MODEL_UNKNOWN`.
- `weight` — both kinds; fit only. A zero-weight row is scored, not fitted.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields`; no `field`. Default label `ATTR_PC_SCORE_<vector or first field>_PC<component>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

One `f64` per record: mean 0, variance 1 over the fit rows<!-- feature: MAT_PCA -->; corr(score, member) = the `MAT_PCA` loading<!-- /feature -->. Null member → null.

<!-- generated: reading-the-output -->

## Gotchas

- Fit is cohort-wide, never per group; groupers or features force buffered.
- Singular fit or N* ≤ p: all null (`PULSE_MATRIX_SINGULAR` / `_INSUFFICIENT_N`).
- Two-pass, never merged.

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: `multivariate-design`, `op-mat-pca`, `op-attr-mahalanobis`
