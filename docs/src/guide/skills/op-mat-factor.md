```yaml
name: op-mat-factor
description: Exploratory factor analysis of a battery — minres, principal-axis or maximum-likelihood extraction from the SMC, orthogonal or oblique rotation, loadings (p × k), communalities, uniquenesses, variance accounted, rmsr, the empirical chi-square, KMO and Bartlett, ML's likelihood chi-square, TLI, RMSEA with its interval and BIC; listwise or pairwise, weighted.
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


| Name | Type | Default | Description |
|---|---|---|---|
| `factors` | any | `kaiser` | k ≤ p, or `"kaiser"` (eigenvalues > 1). |
| `extraction` | `minres` \| `principal_axis` \| `ml` | `minres` | psych `fm` ([`multivariate-outputs`](multivariate-outputs.md)). |
| `rotation` | enum | `none` | Orthogonal `varimax`, `quartimax`, `geomin_orthogonal`, `entropy`; oblique `promax`, `oblimin`, `geomin`. |
| `kappa` | float | `4` | Promax power, > 1. |
| `gamma` | float | `0` | Oblimin: 0 quartimin, 0.5 biquartimin, 1 covarimin. |
| `epsilon` | float | `0.01` | Geomin ε, > 0; larger smooths. |
| `conf_level` | float | `0.9` | `ml` only: RMSEA interval coverage. |
| `repair` | `nearest` | — | Factor the nearest correlation matrix ([`multivariate-design`](multivariate-design.md)). |
| `missing` | `listwise` \| `pairwise` | `listwise` | Pairwise adds `auxiliary.n` ([`matrix-results`](matrix-results.md)). |
| `max_drop_share` | 0–1 | — | Listwise drop-share warning. |
| `fit` | string | — | Emit from a bound vector fit's listwise state, reading no row ([`vector-row-scores`](vector-row-scores.md)). |

`weight`, `encoding`: as [`op-mat-correlation`](op-mat-correlation.md).

## Inputs

Integer / float members.

## Output

`primary`: loadings `F1`…`Fk`. `auxiliary`: `unrotated`, `factor_correlation` (oblique); `vectors`: `communalities`, `uniquenesses`. `scalars`: fit figures (`rmsr`, `kmo`, …); `ml` adds `likelihood_chisq`, `tli`, `rmsea`, `bic`.

## Reading the output

- `primary.values`: Each cell is a loading: how strongly the row's measure is tied to the column's factor. Without a rotation, or with an orthogonal one (varimax, quartimax, geomin_orthogonal, entropy), it is the correlation between the two; under an oblique one (promax, oblimin, geomin) it is the measure's own tie to that factor with the other factors held fixed. A factor is named by the measures with the largest loadings.
  - Caveat: Under an oblique rotation the factors correlate (auxiliary.factor_correlation), so loadings on correlated factors overlap.

## Contract

- Member types (`vector`, `fields`): `f32`, `f64`, `packed_bool`, `u16`, `u32`, `u4`, `u64`, `u8` (`packed_bool` only under `coerce: "binary"`; a `vec_*` field's elements are members); any other is `PULSE_VECTOR_MEMBER_TYPE`.
- Weight: weighted under `frequency` and `probability` weights.
- Streaming: streamable; partial states merge; components `mergeable`.
- Components: `n`, `n_null`, `n_listwise_dropped`; optional `min_pair_n`, `max_pair_n`, `sum_weights`, `n_eff`, `n_weight_invalid`.

## Gotchas

- (p − k)² < p + k: null fit, `PULSE_MATRIX_NOT_IDENTIFIED`.
- Uniqueness ≤ 0: `PULSE_MATRIX_HEYWOOD`; cap hit: `PULSE_MATRIX_NOT_CONVERGED` (`details.solver`, e.g. `gpa_geomin`).
- `ml` needs positive-definite R and enough rows.

## See

- `pulse_examples_search tags=[matrix, correlation-analysis]`
- Skills: [`multivariate-outputs`](multivariate-outputs.md), [`op-mat-pca`](op-mat-pca.md)
