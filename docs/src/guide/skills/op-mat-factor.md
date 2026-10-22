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
