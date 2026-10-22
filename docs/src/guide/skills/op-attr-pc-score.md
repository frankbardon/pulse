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

| Name | Type | Default | Description |
|---|---|---|---|
| `component` | int | (required) | 1-based component to score. |
| `on` | enum | `correlation` | `correlation` or `covariance`. |
| `rotation` | enum | `none` | `none` or orthogonal (`varimax`, `quartimax`, `geomin_orthogonal` + `epsilon`, `entropy`) with `components` = k rotated within (component ≤ k ≤ p); oblique refused (`rotation_oblique_unsupported`). |
| `epsilon` | float | `0.01` | `geomin_orthogonal` only. |
| `components` | int | — | k components rotated within. |
| `fit_on` | any | `filtered` | `"filtered"` or a filterer array picking the FIT rows. |
| `fit` | string | — | A bound vector fit, matched by label; XOR `fit_on` ([`vector-row-scores`](vector-row-scores.md)). |
| `labels` | list | — | Inline `fields`: one label per member. |
| `missing` | enum | `listwise` | `listwise` only. |

`weight` — both kinds; fit only.

Slot: `vector` or `fields`, no `field` ([`vector-row-scores`](vector-row-scores.md)). Default label `ATTR_PC_SCORE_<vector or first field>_PC<component>`.

## Inputs

Members: see `## Contract`; source columns only.

## Output

One `f64` per record: mean 0, variance 1 over the fit rows; corr(score, member) = the `MAT_PCA` loading. Null member → null.

## Reading the output

- `value`: The row's score on the chosen principal component of the fields, standardized so the fitted rows average 0 with a standard deviation of 1: 0 is an average row, +1 is one standard deviation toward the end where the fields with positive loadings are high.
  - Caveat: The sign is a convention (MAT_PCA's: each component's largest loading is positive), not a meaning: check MAT_PCA's loadings on the same fields before calling a high score good or bad.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable.

## Gotchas

- Fit is cohort-wide, never per group: `fit_on` is two-pass (buffered beside groupers / features); `fit` is row-local and merges; a failed fit nulls every score + `PULSE_FIT_UNAVAILABLE` ([`vector-row-scores`](vector-row-scores.md)).
- Singular fit or N* ≤ p: all null (`PULSE_MATRIX_SINGULAR` / `_INSUFFICIENT_N`).

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: [`vector-row-scores`](vector-row-scores.md), [`multivariate-design`](multivariate-design.md), [`op-mat-pca`](op-mat-pca.md), [`op-attr-mahalanobis`](op-attr-mahalanobis.md)
