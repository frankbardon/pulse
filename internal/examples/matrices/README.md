# Matrices

Whole-set matrix operators (`MAT_*`) over virtual vectors
(`Request.Vectors`). Each spec in `matrices` returns one entry in
`Response.Matrices`.

| File | Operator | What it shows |
|---|---|---|
| `01_covariance.json` | `MAT_COVARIANCE` | A vector over three numeric columns and its sample covariance matrix + determinant. |
| `02_correlation.json` | `MAT_CORRELATION` | The same vector's Pearson correlation matrix in upper-triangle encoding + determinant + the two strongest pairs (`top_pairs`). |
| `03_grouped_correlation.json` | `MAT_CORRELATION` + `GROUP_CATEGORY` | One correlation matrix per treatment arm (matrices follow `groups`), each with `group_key` / `group_header`; predict reports the estimated buckets, cells and bytes. |
| `04_pairwise_correlation.json` | `MAT_CORRELATION` | Pairwise deletion (`params.missing: "pairwise"`): each cell over its own rows, plus `auxiliary.n`. |
| `05_weighted_covariance.json` | `MAT_COVARIANCE` | A per-slot probability weight; the weighted floor (`sum_weights`, `n_eff`) in `Components.Matrices`. |
| `06_rank_correlation.json` | `MAT_CORRELATION` | `params.method` `spearman` and `kendall` (tau-b): rank correlation matrices, buffered and serial, equal to the per-pair rank tests. |
| `07_partial_correlation.json` | `MAT_PARTIAL_CORRELATION` | Partial correlations: every pair held fixed for the other members (`control: "all"`), and one pair held fixed for an outside field through a `control` list. |
| `08_reliability.json` | `MAT_RELIABILITY` | Scale reliability of a battery: alpha, standardized alpha, omega, the mean inter-item r and per-item diagnostics (`reverse` + `scale_min` / `scale_max` key reverse-worded items). |
| `09_pca.json` | `MAT_PCA` | Principal components of a battery: rectangular loadings and eigenvectors, eigenvalues with explained / cumulative shares, communalities, KMO and Bartlett's test (`components`: Kaiser default, integer k or a variance share). |
| `10_collinearity.json` | `MAT_COLLINEARITY` | Collinearity diagnostics on candidate predictors (no response): VIF and tolerance, Belsley's condition indices and variance-decomposition proportions, uncentered with the intercept (default) and centered (`center: true`). |
| `11_factor.json` | `MAT_FACTOR` | Exploratory factor analysis: minres (default), principal-axis or maximum-likelihood extraction (with its fit indices), varimax / promax / oblimin rotation (more criteria in `14_rotations.json`), loadings, communalities, uniquenesses, rmsr and the empirical chi-square, KMO and Bartlett (`factors`: integer k or Kaiser default). |
| `12_distance.json` | `MAT_DISTANCE` | Distances between bucket centroids (`between`: any non-date grouper, `GROUP_KMEANS` included): euclidean / sq_euclidean / manhattan / chebyshev / cosine / hellinger / aitchison / mahalanobis (the `linalg/metric` registry), `standardize`, centroids and bucket sizes. |
| `13_correlation_inference.json` | `MAT_CORRELATION` + `MAT_PARTIAL_CORRELATION` | `params.inference`: per-pair `auxiliary.p` and Fisher-z intervals (`ci_lower` / `ci_upper`) on a pairwise Pearson matrix and on a partial correlation with `alpha` 0.1. |
| `14_rotations.json` | `MAT_FACTOR` + `MAT_PCA` | Rotation criteria on a two-factor rating battery (`survey.pulse`): `geomin` (oblique, `epsilon`, `factor_correlation`), `quartimax`, `entropy`, and `geomin_orthogonal` on MAT_PCA (orthogonal criteria only). |
| `15_sem.json` | `MAT_SEM` | A confirmatory factor analysis in lavaan / semopy syntax (`service =~ q1 + q2 + q3`, `value =~ q4 + q5 + q6`): semopy's estimates, standard errors and fit indices (default, its SLSQP and early stop), and the converged fit (`solver: "fisher"`, `information: "rank2"`). |
| `16_sem_multigroup.json` | `MAT_SEM` | A three-factor CFA of `brand_survey.pulse` on the lavaan path: `meanstructure` (intercept `~1` rows, Wald `ci_lower` / `ci_upper`, implied / observed means), then one joint multi-group fit across the two waves — configural, scalar (`group_equal` loadings + intercepts frees wave2's latent means, recovering the fixture's planted value / service shift) and partial (`group_partial` `v2~1`). The ladder as one compose sweep: `compose/09_sem_invariance_sweep.json`. |
| `17_sem_contribution.json` | `MAT_SEM` | Defined parameters (`name := expr`) on the two-wave `brand_survey.pulse` fit under scalar invariance: each driver's contribution to the change in `recommend` (`dv := bv*(mv2 - mv1)`, path times latent mean change), their total and share, with a delta-method SE, p and Wald interval, the slot's own Holm block over every row with a p (`:=` rows included), and the same model under a seeded 200-replicate bootstrap (`resample`, `rng_seed`, `ci_level` 0.9: bootstrap SEs and percentile intervals). |
| `18_sem_ordinal.json` | `MAT_SEM` | The `brand_survey.pulse` CFA with its 1-5 ratings read two ways: lavaan ML with a mean structure (continuous, with a log-likelihood) beside `estimator: "wlsmv"` with every rating `ordered` (two-step thresholds and polychorics, `x \| tk` threshold rows, robust SEs, the scaled-shifted test and `_scaled` indices), then the ordinal model across the two waves with thresholds, loadings and intercepts equal, whose wave2 latent means recover the planted shift. Buffered. |

Run every example:

```
./internal/examples/matrices/run-all.sh
```
