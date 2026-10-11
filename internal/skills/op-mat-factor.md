---
name: op-mat-factor
description: Exploratory factor analysis of a battery — minres, principal-axis or maximum-likelihood extraction from the SMC, varimax / promax / oblimin rotation, loadings (p × k), communalities, uniquenesses, variance accounted, rmsr, the empirical chi-square, KMO and Bartlett, ML's likelihood chi-square, TLI, RMSEA with its interval and BIC; listwise or pairwise, weighted.
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
- `rotation` — `none` (default), `varimax`, `promax`, `oblimin`; `kappa` (4) / `gamma` (0) for promax / oblimin. `repair` — `nearest`.
- `conf_level` — `ml`, default 0.9: RMSEA interval coverage. `missing`, `max_drop_share`, `weight`, `encoding`: `op-mat-correlation`.

## Inputs

Integer / float members.

## Output

`primary`: loadings `F1`…`Fk`. `auxiliary`: `unrotated`, `factor_correlation`; `vectors`: `communalities`, `uniquenesses`. `scalars`: fit figures (`rmsr`, `kmo`, …); `ml` adds `likelihood_chisq`, `tli`, `rmsea`, `bic`.

<!-- generated: reading-the-output -->

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->.

## Gotchas

- (p − k)² < p + k: null fit, `PULSE_MATRIX_NOT_IDENTIFIED`.
- Uniqueness ≤ 0: `PULSE_MATRIX_HEYWOOD`; cap hit: `PULSE_MATRIX_NOT_CONVERGED`.
- `ml` needs positive-definite R and enough rows.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `multivariate-outputs`, `op-mat-pca`
