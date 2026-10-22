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

- `factors` — int k ≤ p or `kaiser` (default). `extraction` — `minres` (default), `principal_axis`, `ml`.
- `rotation` — `none` (default); orthogonal `varimax`, `quartimax`, `geomin_orthogonal`, `entropy`; oblique `promax`, `oblimin`, `geomin`. `kappa` (4) promax, `gamma` (0) oblimin, `epsilon` (0.01) geomin pair. `repair` — `nearest`.
- `conf_level` — `ml`, default 0.9: RMSEA interval coverage. `missing`, `max_drop_share`, `fit`, `weight`, `encoding`: `op-mat-correlation`.

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
