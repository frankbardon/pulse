---
name: op-mat-factor
description: Exploratory factor analysis of a battery — minres, principal-axis or maximum-likelihood extraction from the SMC, orthogonal or oblique rotation, loadings (p × k), communalities, uniquenesses, variance accounted, rmsr, the empirical chi-square, KMO and Bartlett, ML's likelihood chi-square, TLI, RMSEA with its interval and BIC; listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_FACTOR
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

As `op-mat-covariance`; always correlation.

<!-- generated: use-when -->

## Params


| Name | Type | Default | Description |
|---|---|---|---|
| `factors` | any | `kaiser` | k ≤ p, or `"kaiser"` (eigenvalues > 1). |
| `extraction` | `minres` \| `principal_axis` \| `ml` | `minres` | psych `fm` (`multivariate-outputs`). |
| `rotation` | enum | `none` | Orthogonal `varimax`, `quartimax`, `geomin_orthogonal`, `entropy`; oblique `promax`, `oblimin`, `geomin`. |
| `kappa` | float | `4` | Promax power, > 1. |
| `gamma` | float | `0` | Oblimin: 0 quartimin, 0.5 biquartimin, 1 covarimin. |
| `epsilon` | float | `0.01` | Geomin ε, > 0; larger smooths. |
| `conf_level` | float | `0.9` | `ml` only: RMSEA interval coverage. |
| `repair` | `nearest` | — | Factor the nearest correlation matrix (`multivariate-design`). |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise adds `auxiliary.n` (`matrix-results`). |
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |
| `fit` | string | — | Emit from a bound vector fit's listwise state, reading no row (`vector-row-scores`). |

`weight`, `encoding`: as `op-mat-correlation`.

## Inputs

Integer / float members.

## Output

`primary`: loadings `F1`…`Fk`. `auxiliary`: `unrotated`, `factor_correlation` (oblique); `vectors`: `communalities`, `uniquenesses`. `scalars`: fit figures (`rmsr`, `kmo`, …); `ml` adds `likelihood_chisq`, `tli`, `rmsea`, `bic`.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- (p − k)² < p + k: null fit, `PULSE_MATRIX_NOT_IDENTIFIED`.
- Uniqueness ≤ 0: `PULSE_MATRIX_HEYWOOD`; cap hit: `PULSE_MATRIX_NOT_CONVERGED` (`details.solver`, e.g. `gpa_geomin`).
- `ml` needs positive-definite R and enough rows.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `multivariate-outputs`, `op-mat-pca`
