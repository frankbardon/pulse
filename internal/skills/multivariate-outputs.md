---
name: multivariate-outputs
description: Long-form output and parameter detail for the multivariate operators — how rotated PCA and factor loadings, the maximum-likelihood fit indices, centroid distances, row scores and the vector tests lay out their figures. Topical design; the atomic op-mat-*, op-attr-* and op-test-* skills keep the short form.
type: guide
kind: design
applies_to: process, compose, predict
covers: [PCA rotation, factor fit indices, RMSEA, centroid distance, factor score, Hotelling, MANOVA, vector test details]
requires: [capability:matrices]
---

# Multivariate outputs in detail

`multivariate-design` picks the operator; the atomic skill lists its params and a one-line output. This file holds the longer reading of the figures. Everything here is descriptive layout, not new params.

<!-- feature: MAT_PCA -->
## PCA

`primary` holds the loadings (eigenvector · √λ), `rectangular`, columns `PC1`…`PCk`, always the full p rows. `auxiliary.eigenvectors` has the same shape.

With an orthogonal `rotation` (k ≥ 2: `varimax`, `stats::varimax` Kaiser-normalized; `quartimax`, `geomin_orthogonal`, `entropy`, GPArotation's; an oblique name is refused) `primary` holds the rotated loadings: columns ordered by descending variance accounted, each summing positive. `auxiliary.unrotated` keeps the originals and `vectors.rotated_variance` gives each rotated column's Σ loading². Eigenvalues, shares, communalities and eigenvectors stay unrotated, so `explained_variance` is still per eigenvalue: read `rotated_variance` for the rotated split, and expect `PC1` to be the column with the most variance, not the first eigenvector.
<!-- /feature -->

<!-- feature: MAT_FACTOR -->
## Factor analysis

`primary` holds the loadings (`F1`…`Fk`, rotated when set); `auxiliary.unrotated` appears on rotated runs and `factor_correlation` (k × k) on an oblique rotation (promax, oblimin, geomin). `kappa` is the promax power (default 4), `gamma` the oblimin γ (default 0), `epsilon` the geomin pair's ε (default 0.01); any other rotation refuses them. Geomin may stop in a local optimum (one start, at the unrotated loadings, as GPArotation). `extraction` follows psych `fm`: `minres`, `principal_axis` (`pa`) or `ml` (`factanal`, ψ held in [0.005, 1]).

`vectors` carry `communalities`, `uniquenesses`, `variance_accounted`; pairwise runs add `auxiliary.n`. `scalars` always carry `rmsr`, `empirical_chisq`, `df`, `empirical_p`, `kmo`, `bartlett_*`, `iterations`, `converged` (1/0) and `factors_retained`. `empirical_chisq` measures the residual size; it is not a likelihood test.

`ml` adds the likelihood fit: `likelihood_chisq` (Bartlett-corrected, on `df`), `likelihood_p`, `tli`, `rmsea` with `rmsea_lower` / `rmsea_upper` (psych `RMSEA` lower / upper; `conf_level` sets the coverage, default 0.9) and `bic`, null when `df` ≤ 0. These need N* > 1 + (2p + 5)/6 + 2k/3 (`PULSE_MATRIX_INSUFFICIENT_N`) and a positive-definite R (`PULSE_MATRIX_SINGULAR`). Identification: (p − k)² ≥ p + k, so 1 factor needs 3 members and 2 need 5 (`PULSE_MATRIX_NOT_IDENTIFIED`). A uniqueness held at 0.005 is a Heywood case, kept with `PULSE_MATRIX_HEYWOOD`.
<!-- /feature -->

<!-- feature: MAT_DISTANCE -->
## Centroid distances

`between` takes a grouper spec as in `groups` (`{type, field | vector | fields, params, include, interval}`): any built-in, extension groupers too; no own `weight`, and `tz` only on a date-family grouper (else the request `time_zone`; predict echoes it at `matrices[i].params.between`). `primary` is k × k, `square_symmetric`, keyed by bucket label in the grouper's order (a clustering grouper labels buckets `cluster_1`… by size), zero diagonal. `auxiliary.centroids` is k × p; `centroids_standardized` appears with `standardize`, `pooled_covariance` with `mahalanobis` (computed over the pooled within-bucket covariance, on raw centroids). `vectors.sizes` is Σw per bucket and `counts` the rows. `metric` names come from the shared registry (manifest `matrix.metrics`, consumer `MAT_DISTANCE`). `cosine` is 1 − cos; the rest follow `stats::dist`, an undefined coordinate dropped and the sums rescaled by p/p_observed.

Components add `metric`, `standardize`, `buckets` and `n_null_key` (rows in no bucket, excluded). A massless bucket gives a null centroid and `PULSE_MATRIX_INSUFFICIENT_N` scope `buckets`; mahalanobis with N* ≤ k uses scope `pooled`.
<!-- /feature -->

<!-- feature: ATTR_FACTOR_SCORE -->
## Factor scores

One `f64` per record: zᵀw on members standardized by the fit. Regression weights are w = R⁻¹LΦ, Bartlett weights w = U⁻²L(LᵀU⁻²L)⁻¹ (psych `factor.scores`). Default label `ATTR_FACTOR_SCORE_<vector or first field>_F<factor>`. The score has mean 0 over the fit rows; a null member gives a null score. The fit takes the factor knobs (`factors`, `extraction`, `rotation`, `kappa`, `gamma`, `epsilon`; no `repair`).

All scores null with a warning: a singular fit (`PULSE_MATRIX_SINGULAR`), N* ≤ p (`PULSE_MATRIX_INSUFFICIENT_N`), k not identified or `"kaiser"` keeping fewer than `factor` (`PULSE_MATRIX_NOT_IDENTIFIED`), Bartlett over a uniqueness ≤ 0 (`PULSE_MATRIX_HEYWOOD`). Scored with a warning: a Heywood case under regression, or an iteration cap (`PULSE_MATRIX_NOT_CONVERGED`, kept beside a refusal too).
<!-- /feature -->

<!-- feature: TEST_HOTELLING_T2 -->
## Hotelling's T²

`Statistic` = T², `DF` = p. `Details`: `f`, `df1`, `df2`, `n`, `n_dropped`, `members`; one-sample adds `mean`, `mu`; paired `members2`, `mean_diff`, `mu`; two-sample `groups` (sorted), `means`, `mean_diff`. `Variant`: `one_sample`, `paired`, `two_sample_pooled`, `two_sample_unequal`. `variance: unequal` is Krishnamoorthy–Yu (fractional `df2`), Welch's t at p = 1, and refused on one-sample / paired. Needs n > p (one-sample, paired), n₁+n₂ > p+1 (pooled), n > p per level (unequal). `unequal` at p = 1 is Welch's t. Weighted: moments on w*, N* = Σw or n_eff, and `Details` add `sum_weights` (+ `n_eff`) shaped like `n`.
<!-- /feature -->

<!-- feature: TEST_MANOVA -->
## MANOVA

`Statistic` = Pillai's V, `DF` its df1, `PValue` its p. `Details`: `f`, `df1`, `df2`, `members`, `groups` (sorted), `n`, `n_dropped`, `means`, `df_hypothesis` (k − 1), `df_residual` (N* − k), `eigenvalues` (of E⁻¹H) and `pillai` / `wilks` / `hotelling_lawley` / `roy`, each `{statistic, f, df1, df2, p_value}`. `roy.upper_bound` is true: Roy's p is a lower bound. A null `p_value` means R would print NA. Needs n ≥ p + k complete rows (Σw under `frequency`). Weighted: N*_g = Σw or n_eff, and `Details` add per-level `sum_weights` (+ `n_eff`).
<!-- /feature -->

## See

- `multivariate-design` — which operator answers which question.
- `matrix-results` — the matrix slot, encodings and refusals.
