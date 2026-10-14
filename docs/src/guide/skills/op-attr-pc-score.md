```yaml
name: op-attr-pc-score
description: Per-row standardized score on one principal component of a vector of numeric members, with the PCA matrix operator's sign and order and optional orthogonal rotation; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_PC_SCORE
type: reference
applies_to: process, compose, predict
examples_tags: [multivariate, feature-engineering, streaming-friendly]
```

Row-level scalars; no `Response.Components`.

## Use when

Adds to every row its score on one principal component of several related numeric fields: one number standing in for what they share.

Questions it answers:

- What is each respondent's score on the main dimension behind these rating questions?
- Can I replace these five correlated measures with one summary column for a later model?

Use something else:

- `ATTR_MAHALANOBIS` when you want how unusual a row is across the fields.

## Params

- `component` — required, 1-based. `on` — `correlation` (default) or `covariance`. `missing` — `listwise` only.
- `rotation` — `none` (default) or orthogonal (`varimax`, `quartimax`, `geomin_orthogonal` + `epsilon`, `entropy`) with `components` = k rotated within (component ≤ k ≤ p); oblique refused (`rotation_oblique_unsupported`).
- `fit_on` — `"filtered"` (default) or a filterer array picking the FIT rows; every filtered row is scored.
- `fit` — a bound vector fit's name (a compose `fits` entry over one matrix or fitted-attribute battery, or a loaded `populations` entry: [`compose-fits`](compose-fits.md)), scored against instead of fitting (XOR `fit_on`: `fit_and_fit_on`); its own weight basis governs. Matched BY LABEL to the members (`labels` — inline `fields` only); mismatch `PULSE_VECTOR_REFERENCE_MISMATCH`, unbound `PULSE_MODEL_UNKNOWN`, a regression's name `PULSE_MODEL_KIND_MISMATCH`.
- `weight` — both kinds; fit only. A zero-weight row is scored, not fitted.

Slot: exactly one of `vector` (a `vectors` entry) / `fields`; no `field`. Default label `ATTR_PC_SCORE_<vector or first field>_PC<component>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64`; source columns only.

## Output

One `f64` per record: mean 0, variance 1 over the fit rows; corr(score, member) = the `MAT_PCA` loading. Null member → null.

## Reading the output

- `value`: The row's score on the chosen principal component of the fields, standardized so the fitted rows average 0 with a standard deviation of 1: 0 is an average row, +1 is one standard deviation toward the end where the fields with positive loadings are high.
  - Caveat: The sign is a convention (MAT_PCA's: each component's largest loading is positive), not a meaning: check MAT_PCA's loadings on the same fields before calling a high score good or bad.

## Gotchas

- Fit is cohort-wide, never per group; with `fit_on`, groupers or features force buffered — two-pass, never merged.
- With `fit` it is row-local: one pass, streams beside groupers and features, merges (shards, chain stages); same scores.
- Singular fit or N* ≤ p: all null (`PULSE_MATRIX_SINGULAR` / `_INSUFFICIENT_N`).

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: [`multivariate-design`](multivariate-design.md), [`op-mat-pca`](op-mat-pca.md), [`op-attr-mahalanobis`](op-attr-mahalanobis.md)
