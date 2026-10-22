---
name: op-mat-collinearity
description: Collinearity check of candidate regression predictors (no response) — VIF and tolerance per predictor, Belsley's condition indices and variance-decomposition proportions (uncentered with the intercept, or centered), listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_COLLINEARITY
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot and members as for `op-mat-covariance`. Members are the predictors only.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `center` | bool | `false` | Belsley on centered predictors (no intercept). |
| `repair` | `nearest` | — | Read the nearest correlation matrix. |
| `missing` | `listwise` \| `pairwise` | `listwise` | listwise drops a row with any member null; pairwise computes each pair over the rows where both are present and adds auxiliary.n (pairwise N). |
| `max_drop_share` | float | — | Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1). |
| `fit` | string | — | A bound vector fit's name (a Compose fits entry over one MAT_* or fitted-attribute battery): the matrix is emitted from that fit's listwise state, folding no row; matched to the members BY LABEL; its own weight basis governs. |

## Inputs

Integer / float members.

## Output

`primary`: the predictors' correlation R. `vectors`: `vif` (diag R⁻¹), `tolerance` (1/VIF), `condition_indices` (ascending, first 1). `auxiliary.variance_decomposition`: kind `rectangular`, a row per variable (`(intercept)` first unless `center`), columns `D1`…`Dq` in condition-index order, rows sum to 1; pairwise `auxiliary.n`. `scalars`: `condition_number`, `max_vif`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Uncentered (default) flags collinearity with the intercept; VIF is the same either way.
- Exactly or near-exactly collinear predictors (rank tolerance): fatal `PULSE_MATRIX_SINGULAR` with `dependent_fields`. Non-PSD pairwise R: fatal `PULSE_MATRIX_NOT_PSD` unless `repair`.
- VIF unbanded: 5 / 10 are rules of thumb only.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `op-mat-correlation`, `op-reg-ols`, `weighting`
