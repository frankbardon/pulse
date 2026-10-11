---
name: op-mat-pca
description: Principal component analysis of a battery — loadings and eigenvectors (p × k, rectangular), every eigenvalue with explained / cumulative shares, communalities, KMO (overall and per member) and Bartlett's sphericity test; correlation or covariance input, listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_PCA
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

As `op-mat-covariance`.

<!-- generated: use-when -->

## Params

- `on` — `correlation` (default) or `covariance`. `rotation` — `none`, `varimax` (k ≥ 2). `repair` — `nearest`.
- `components` — int k (≤ p), `kaiser` (λ > 1, default) or `{variance: s}`; required on `covariance`.
- `missing`, `max_drop_share`, `weight`, `encoding`: `op-mat-correlation`.

## Inputs

Integer / float members.

## Output

`primary`: loadings (eigenvector · √λ), `PC1`…`PCk`. `auxiliary`: `eigenvectors`, `n` (pairwise), `unrotated`. `vectors`: `eigenvalues`, `explained_variance`, `cumulative`, `communalities`, `kmo_msa`, `rotated_variance`. `scalars`: `kmo`, `bartlett_*`, `components_retained`.

<!-- generated: reading-the-output -->

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->.

## Gotchas

- Signs are free. Rotated: read `rotated_variance`.
- Bartlett's n: rows, Σw or n_eff, pairwise the smallest pair.
- Singular: KMO, Bartlett null (`PULSE_MATRIX_SINGULAR`). Non-PSD pairwise: `PULSE_MATRIX_NOT_PSD` unless `repair`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `multivariate-outputs`, `op-mat-correlation`
