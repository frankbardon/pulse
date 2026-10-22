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


| Name | Type | Default | Description |
|---|---|---|---|
| `on` | `correlation` \| `covariance` | `correlation` | Covariance keeps units: a large-spread member dominates. |
| `components` | any | `kaiser` | Int k (≤ p), `kaiser` (λ > 1, default) or `{variance: s}`; required on `covariance`. |
| `rotation` | enum | `none` | k ≥ 2, orthogonal: `varimax`, `quartimax`, `geomin_orthogonal`, `entropy`. |
| `epsilon` | float | `0.01` | Geomin ε, > 0; larger smooths. |
| `repair` | `nearest` | — | Decompose the nearest correlation matrix (rescaled on `covariance`; `multivariate-design`). |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise adds `auxiliary.n` (`matrix-results`). |
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |
| `fit` | string | — | Emit from a bound vector fit's listwise state, reading no row (`vector-row-scores`). |

`weight`, `encoding`: as `op-mat-correlation`. Oblique names are refused: `rotation_oblique_unsupported`.

## Inputs

Integer / float members.

## Output

`primary`: loadings (eigenvector · √λ), `PC1`…`PCk`. `auxiliary`: `eigenvectors`, `n` (pairwise), `unrotated`. `vectors`: `eigenvalues`, `explained_variance`, `cumulative`, `communalities`, `kmo_msa`, `rotated_variance`. `scalars`: `kmo`, `bartlett_*`, `components_retained`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Signs are free; rotated, read `rotated_variance`.
- Bartlett's n: rows, Σw or n_eff, pairwise the smallest pair's own. A pairwise weighted covariance scales each entry by its own pair.
- Singular: KMO, Bartlett null (`PULSE_MATRIX_SINGULAR`). Non-PSD pairwise: `PULSE_MATRIX_NOT_PSD` unless `repair`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `multivariate-outputs`, `op-mat-correlation`
