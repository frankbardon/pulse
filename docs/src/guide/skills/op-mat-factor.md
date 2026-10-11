```yaml
name: op-mat-factor
description: Exploratory factor analysis of a battery — minres or principal-axis extraction from the SMC, varimax / promax / oblimin rotation, loadings (p × k), communalities, uniquenesses, variance accounted, rmsr, the empirical chi-square, KMO and Bartlett; listwise or pairwise, weighted.
kind: operator
category: MAT
operator: MAT_FACTOR
type: reference
applies_to: process, compose, predict
examples_tags: [matrix, correlation-analysis]
```

Slot and members as for [`op-mat-covariance`](op-mat-covariance.md); always the correlation.

## Use when

Which hidden qualities a set of related measures reflects, and how strongly each measure is tied to each, before building scale scores.

Questions it answers:

- Do these twelve attitude questions reflect two or three underlying qualities, and which questions go with which?
- Which items in this battery tie weakly to every quality and could be dropped?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `factors` | int \| `kaiser` | `kaiser` | k ≤ p, or eigenvalues of R above 1. |
| `extraction` | `minres` \| `principal_axis` | `minres` | psych `fm` `minres` / `pa`. |
| `rotation` | `none` \| `varimax` \| `promax` \| `oblimin` | `none` | Shared rotation engine. |
| `kappa` / `gamma` | float | 4 / 0 | Promax power / oblimin γ; refused with any other rotation. |
| `repair` | `nearest` | none | Factor the nearest correlation matrix. |
| `missing`, `max_drop_share`, `weight`, `encoding` | | | As [`op-mat-correlation`](op-mat-correlation.md). |

## Inputs

Integer / float members.

## Output

`primary`: loadings, `rectangular`, `F1`…`Fk` (rotated when set). `auxiliary.unrotated` (rotated runs), `factor_correlation` (promax / oblimin, k × k), pairwise `n`. `vectors`: `communalities`, `uniquenesses`, `variance_accounted`. `scalars`: `rmsr`, `empirical_chisq`, `df`, `empirical_p`, `kmo`, `bartlett_*`, `iterations`, `converged` (1/0), `factors_retained`.

## Reading the output

- `primary.values`: Each cell is a loading: how strongly the row's measure is tied to the column's factor. Without a rotation, or with varimax, it is the correlation between the two; under promax or oblimin it is the measure's own tie to that factor with the other factors held fixed. A factor is named by the measures with the largest loadings.
- `scalars.bartlett_p`: a p-value. Below alpha (0.05 unless the request sets another) the result is called significant; that is not the same as important, so read the effect size for how big it is.

## Components

`n`, `n_null`, `n_listwise_dropped`; pairwise `min_pair_n` / `max_pair_n`; weighted `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- (p − k)² < p + k: null fit, `PULSE_MATRIX_NOT_IDENTIFIED` (1 factor needs 3 members, 2 need 5).
- Uniqueness ≤ 0: kept, `PULSE_MATRIX_HEYWOOD`. Cap hit: `PULSE_MATRIX_NOT_CONVERGED`.
- `empirical_chisq` is a residual size, not a likelihood test.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`multivariate-design`](multivariate-design.md), [`op-mat-pca`](op-mat-pca.md), [`op-mat-reliability`](op-mat-reliability.md), [`matrix-results`](matrix-results.md)
