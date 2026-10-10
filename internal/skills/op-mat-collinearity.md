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
| `repair` | `nearest` | none | Read the nearest correlation matrix. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As `op-mat-correlation`. |

## Inputs

Integer / float members.

## Output

`primary`: the predictors' correlation R. `vectors`: `vif` (diag R⁻¹), `tolerance` (1/VIF), `condition_indices` (ascending, first 1). `auxiliary.variance_decomposition`: kind `rectangular`, a row per variable (`(intercept)` first unless `center`), columns `D1`…`Dq` in condition-index order, rows sum to 1; pairwise `auxiliary.n`. `scalars`: `condition_number`, `max_vif`.

<!-- generated: reading-the-output -->

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->.

## Gotchas

- Uncentered (default) flags collinearity with the intercept; VIF is the same either way.
- Exactly collinear predictors: fatal `PULSE_MATRIX_SINGULAR` with `dependent_fields`. Non-PSD pairwise R: fatal `PULSE_MATRIX_NOT_PSD` unless `repair`.
- VIF unbanded: 5 / 10 are rules of thumb only.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `matrix-results`, `op-mat-correlation`, `op-reg-ols`, `weighting`
