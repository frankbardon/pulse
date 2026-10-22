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
| `factors` | any | `kaiser` | How many factors to extract: a positive integer k (at most the member count; a k the members cannot identify, (p - k)^2 < p + k, returns a null fit with PULSE_MATRIX_NOT_IDENTIFIED) or "kaiser" (as many as the correlation has eigenvalues above 1). |
| `extraction` | `minres` \| `principal_axis` \| `ml` | `minres` | minres: minimise the off-diagonal residuals over the communalities (psych fm = "minres"; Pulse solves to the exact optimum, where psych's optimiser stops about 1e-5 short). |
| `rotation` | enum | `none` | `none` (default); orthogonal `varimax`, `quartimax`, `geomin_orthogonal`, `entropy`; oblique `promax`, `oblimin`, `geomin`. `kappa`, `gamma`, `epsilon` tune promax, oblimin, geomin. |
| `kappa` | float | `4` | Promax power, a finite number above 1 (stats::promax m). Accepted with rotation promax only. |
| `gamma` | float | `0` | Oblimin gamma, finite: 0 quartimin (the default), 0.5 biquartimin, 1 covarimin. |
| `epsilon` | float | `0.01` | Geomin epsilon, a finite number above 0 (GPArotation delta; default 0.01): larger smooths the criterion. |
| `conf_level` | float | `0.9` | `ml` only: RMSEA interval coverage. |
| `repair` | `nearest` | — | nearest: factor the nearest correlation matrix when the (pairwise) correlation is not PSD, with a PULSE_MATRIX_NOT_PSD warning carrying frobenius_adjustment. |
| `missing` | `listwise` \| `pairwise` | `listwise` | listwise drops a row with any member null; pairwise computes each pair over the rows where both are present and adds auxiliary.n (pairwise N). |
| `max_drop_share` | float | — | Listwise only, no default: warn PULSE_MATRIX_LISTWISE_HEAVY_DROP when the share of rows dropped exceeds it (0 to 1). |
| `fit` | string | — | A bound vector fit's name (a Compose fits entry over one MAT_* or fitted-attribute battery): the matrix is emitted from that fit's listwise state, folding no row; matched to the members BY LABEL; its own weight basis governs. |

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
