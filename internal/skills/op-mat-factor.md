---
name: op-mat-factor
description: Exploratory factor analysis of a battery — minres or principal-axis extraction from the SMC, varimax / promax / oblimin rotation, loadings (p × k), communalities, uniquenesses, variance accounted, rmsr, the empirical chi-square, KMO and Bartlett; listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_FACTOR
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
---

Slot and members as for `op-mat-covariance`; always the correlation.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `factors` | int \| `kaiser` | `kaiser` | k ≤ p, or eigenvalues of R above 1. |
| `extraction` | `minres` \| `principal_axis` | `minres` | psych `fm` `minres` / `pa`. |
| `rotation` | `none` \| `varimax` \| `promax` \| `oblimin` | `none` | Shared rotation engine. |
| `kappa` / `gamma` | float | 4 / 0 | Promax power / oblimin γ; refused with any other rotation. |
| `repair` | `nearest` | none | Factor the nearest correlation matrix. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As `op-mat-correlation`. |

## Inputs

Integer / float members.

## Output

`primary`: loadings, `rectangular`, `F1`…`Fk` (rotated when set). `auxiliary.unrotated` (rotated runs), `factor_correlation` (promax / oblimin, k × k), pairwise `n`. `vectors`: `communalities`, `uniquenesses`, `variance_accounted`. `scalars`: `rmsr`, `empirical_chisq`, `df`, `empirical_p`, `kmo`, `bartlett_*`, `iterations`, `converged` (1/0), `factors_retained`.

<!-- generated: reading-the-output -->

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`<!-- feature: capability:weighting -->; weighted `sum_weights`, `n_eff`, `n_weight_invalid`<!-- /feature -->.

## Gotchas

- (p − k)² < p + k: null fit, `PULSE_MATRIX_NOT_IDENTIFIED` (1 factor needs 3 members, 2 need 5).
- Uniqueness ≤ 0: kept, `PULSE_MATRIX_HEYWOOD`. Cap hit: `PULSE_MATRIX_NOT_CONVERGED`.
- `empirical_chisq` is a residual size, not a likelihood test.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: `multivariate-design`, `op-mat-pca`, `op-mat-reliability`, `matrix-results`
