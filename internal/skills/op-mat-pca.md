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
| `on` | `correlation` \| `covariance` | `correlation` | correlation: analyse the correlation matrix (every member on one scale); covariance: the covariance matrix (members in their own units, so a large-spread member dominates; params.components is then required). |
| `components` | any | `kaiser` | Int k (≤ p), `kaiser` (λ > 1, default) or `{variance: s}`; required on `covariance`. |
| `rotation` | enum | `none` | `none` (default); with k ≥ 2 orthogonal `varimax`, `quartimax`, `geomin_orthogonal` (`epsilon`), `entropy`. Oblique names are refused. |
| `epsilon` | float | `0.01` | Geomin epsilon, a finite number above 0 (GPArotation delta; default 0.01): larger smooths the criterion. |
| `repair` | `nearest` | — | nearest: decompose the nearest correlation matrix (scaled back to the covariance's variances on a covariance) when the (pairwise) input is not PSD, with a PULSE_MATRIX_NOT_PSD warning carrying frobenius_adjustment. |
| `missing` | `listwise` \| `pairwise` | `listwise` | listwise drops a row with any member null; pairwise computes each pair over the rows where both are present and adds auxiliary.n (pairwise N). |
| `max_drop_share` | float | — | Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1). |
| `fit` | string | — | A bound vector fit's name (a Compose fits entry over one MAT_* or fitted-attribute battery): the matrix is emitted from that fit's listwise state, folding no row; matched to the members BY LABEL; its own weight basis governs. |

`weight`, `encoding`: as `op-mat-correlation`. Oblique rotation names are refused: `rotation_oblique_unsupported`.

## Inputs

Integer / float members.

## Output

`primary`: loadings (eigenvector · √λ), `PC1`…`PCk`. `auxiliary`: `eigenvectors`, `n` (pairwise), `unrotated`. `vectors`: `eigenvalues`, `explained_variance`, `cumulative`, `communalities`, `kmo_msa`, `rotated_variance`. `scalars`: `kmo`, `bartlett_*`, `components_retained`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Signs are free. Rotated: read `rotated_variance`.
- Bartlett's n: rows, Σw or n_eff, pairwise the smallest pair's own. A pairwise weighted covariance scales each entry by its own pair.
- Singular: KMO, Bartlett null (`PULSE_MATRIX_SINGULAR`). Non-PSD pairwise: `PULSE_MATRIX_NOT_PSD` unless `repair`.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `multivariate-outputs`, `op-mat-correlation`
