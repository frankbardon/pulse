```yaml
name: op-mat-factor
description: Exploratory factor analysis of a battery — minres, principal-axis or maximum-likelihood extraction from the SMC, varimax / promax / oblimin rotation, loadings (p × k), communalities, uniquenesses, variance accounted, rmsr, the empirical chi-square, KMO and Bartlett, ML's likelihood chi-square, TLI, RMSEA with its interval and BIC; listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_FACTOR
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
```

As [`op-mat-covariance`](op-mat-covariance.md); always correlation.

## Use when

Which hidden qualities a set of related measures reflects, and how strongly each measure is tied to each, before building scale scores.

Questions it answers:

- Do these twelve attitude questions reflect two or three underlying qualities, and which questions go with which?
- Which items in this battery tie weakly to every quality and could be dropped?

## Params

- `factors` — int k ≤ p or `kaiser` (default). `extraction` — `minres` (default), `principal_axis`, `ml`.
- `rotation` — `none` (default), `varimax`, `promax`, `oblimin`; `kappa` (4) / `gamma` (0) for promax / oblimin. `repair` — `nearest`.
- `conf_level` — `ml`, default 0.9: RMSEA interval coverage. `missing`, `max_drop_share`, `fit`, `weight`, `encoding`: [`op-mat-correlation`](op-mat-correlation.md).

## Inputs

Integer / float members.

## Output

`primary`: loadings `F1`…`Fk`. `auxiliary`: `unrotated`, `factor_correlation`; `vectors`: `communalities`, `uniquenesses`. `scalars`: fit figures (`rmsr`, `kmo`, …); `ml` adds `likelihood_chisq`, `tli`, `rmsea`, `bic`.

## Reading the output

- `primary.values`: Each cell is a loading: how strongly the row's measure is tied to the column's factor. Without a rotation, or with varimax, it is the correlation between the two; under promax or oblimin it is the measure's own tie to that factor with the other factors held fixed. A factor is named by the measures with the largest loadings.
- `scalars.bartlett_p`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- (p − k)² < p + k: null fit, `PULSE_MATRIX_NOT_IDENTIFIED`.
- Uniqueness ≤ 0: `PULSE_MATRIX_HEYWOOD`; cap hit: `PULSE_MATRIX_NOT_CONVERGED`.
- `ml` needs positive-definite R and enough rows.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`multivariate-outputs`](multivariate-outputs.md), [`op-mat-pca`](op-mat-pca.md)
