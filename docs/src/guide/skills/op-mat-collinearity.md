```yaml
name: op-mat-collinearity
description: Collinearity check of candidate regression predictors (no response) — VIF and tolerance per predictor, Belsley's condition indices and variance-decomposition proportions (uncentered with the intercept, or centered), listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_COLLINEARITY
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
```

Slot and members as for [`op-mat-covariance`](op-mat-covariance.md). Members are the predictors only.

## Use when

Whether candidate predictors overlap so much that a regression on them would give unstable coefficients, and which ones are tangled.

Questions it answers:

- Before I model satisfaction on these eight ratings, are any of them so related that their effects cannot be told apart?
- Which of these predictors are near-duplicates of the others?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `center` | bool | `false` | Belsley on centered predictors (no intercept). |
| `repair` | `nearest` | none | Read the nearest correlation matrix. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As [`op-mat-correlation`](op-mat-correlation.md). |

## Inputs

Integer / float members.

## Output

`primary`: the predictors' correlation R. `vectors`: `vif` (diag R⁻¹), `tolerance` (1/VIF), `condition_indices` (ascending, first 1). `auxiliary.variance_decomposition`: kind `rectangular`, a row per variable (`(intercept)` first unless `center`), columns `D1`…`Dq` in condition-index order, rows sum to 1; pairwise `auxiliary.n`. `scalars`: `condition_number`, `max_vif`.

## Reading the output

- `scalars.max_vif`: The largest variance inflation factor among the predictors (each one's is also listed): how many times larger that predictor's coefficient variance is than it would be if it were unrelated to the other predictors. 1 means no overlap; tolerance, its reciprocal, is the share of the predictor's spread the others do not explain.
- `primary.values`: The predictors' correlation table that the variance inflation factors and the centered diagnostics are read from: each off-diagonal cell is the correlation of its row and column predictors, from -1 to +1.

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- Uncentered (default) flags collinearity with the intercept; VIF is the same either way.
- Exactly collinear predictors: fatal `PULSE_MATRIX_SINGULAR` with `dependent_fields`. Non-PSD pairwise R: fatal `PULSE_MATRIX_NOT_PSD` unless `repair`.
- VIF unbanded: 5 / 10 are rules of thumb only.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`matrix-results`](matrix-results.md), [`op-mat-correlation`](op-mat-correlation.md), [`op-reg-ols`](op-reg-ols.md), [`weighting`](weighting.md)
