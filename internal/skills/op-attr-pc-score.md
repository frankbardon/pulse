---
name: op-attr-pc-score
description: Per-row standardized score on one principal component of a vector of numeric members, with the PCA matrix operator's sign and order and optional orthogonal rotation; fit_on fits on a subset and still scores every row.
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

| Name | Type | Default | Description |
|---|---|---|---|
| `component` | int | (required) | 1-based component to score. |
| `on` | enum | `correlation` | `correlation` or `covariance`. |
| `rotation` | enum | `none` | `none` or orthogonal (`varimax`, `quartimax`, `geomin_orthogonal` + `epsilon`, `entropy`) with `components` = k rotated within (component ≤ k ≤ p); oblique refused (`rotation_oblique_unsupported`). |
| `epsilon` | float | `0.01` | `geomin_orthogonal` only. |
| `components` | int | — | k components rotated within. |
| `fit_on` | any | `filtered` | `"filtered"` or a filterer array picking the FIT rows. |
| `fit` | string | — | A bound vector fit, matched by label; XOR `fit_on` (`vector-row-scores`). |
| `labels` | list | — | Inline `fields`: one label per member. |
| `missing` | enum | `listwise` | `listwise` only. |

`weight` — both kinds; fit only.

Slot: `vector` or `fields`, no `field` (`vector-row-scores`). Default label `ATTR_PC_SCORE_<vector or first field>_PC<component>`.

## Inputs

Members: see `## Contract`; source columns only.

## Output

One `f64` per record: mean 0, variance 1 over the fit rows<!-- feature: MAT_PCA -->; corr(score, member) = the `MAT_PCA` loading<!-- /feature -->. Null member → null.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Fit is cohort-wide, never per group: `fit_on` is two-pass (buffered beside groupers / features); `fit` is row-local and merges; a failed fit nulls every score + `PULSE_FIT_UNAVAILABLE` (`vector-row-scores`).
- Singular fit or N* ≤ p: all null (`PULSE_MATRIX_SINGULAR` / `_INSUFFICIENT_N`).

## See

- `pulse_examples_search tags=[multivariate, feature-engineering]`
- Skills: `vector-row-scores`, `multivariate-design`, `op-mat-pca`, `op-attr-mahalanobis`
