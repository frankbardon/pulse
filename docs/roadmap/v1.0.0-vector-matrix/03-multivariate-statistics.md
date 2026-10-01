# 03 — Multivariate statistics

This document proposes a new operator category, `MAT_*`: whole-cohort (or per-group) operators that produce a `MatrixResult`. It also lists additions to the existing `TEST_*`, `ATTR_*`, `AGG_*`, `GROUP_*`, `WIN_*` and `REG_*` categories that consume vectors.

All `MAT_*` operators share these inputs:
- `vector` **or** `fields`, resolving to `p` numeric columns;
- an optional `weight` field;
- an optional `missing` mode (`listwise` by default, or `pairwise` where the operator allows it);
- `params` specific to the operator.

They honour `Request.Filterers` and `Request.Groups` exactly as aggregators do.

Legend for each entry: **Tier** C/S/P · **Stream**: does it ride the mergeable co-moment accumulator (F2)?

---

## Matrix operators (`MAT_*`)

### `MAT_COVARIANCE` — Tier C · Stream ✔
- **Output.** `primary` is the p×p covariance matrix (population or sample, via `params.ddof`). `vectors.mean` and `vectors.sd` are the per-variable mean and standard deviation. `auxiliary.n` holds the pairwise N under pairwise deletion.
- **Why.** It is the base for everything else, and on its own it gives portfolio-style risk tables and variance decomposition.

### `MAT_CORRELATION` — Tier C · Stream ✔ (Pearson) / ✘ (Spearman, Kendall)
- **Params.** `method`: `pearson` | `spearman` | `kendall`.
- **Output.**
  - `primary`: the r matrix.
  - `auxiliary.p`: p-values.
  - `auxiliary.n`: N per pair.
  - `vectors.top_pairs`: optional ranked pairs.
- **Consistency.** Byte-equal to N² runs of `TEST_PEARSON_R` / `TEST_SPEARMAN_R` / `TEST_KENDALL_TAU` on the same pairs. That parity is the acceptance test, mirroring how χ² overlays are pinned to `TEST_CHISQ`.
- **Audience.** All four. It is the single most-requested multivariate output.

### `MAT_PARTIAL_CORRELATION` — Tier C · Stream ✔
- **What it computes.** Correlation between each pair of variables after controlling for all the others (`params.control: "all"`) or for a named control list.
- **Method.** Uses the precision matrix (the inverse of the covariance matrix).
- **Why.** Separates direct association from shared drivers. Example: does price perception correlate with satisfaction *after* controlling for quality perception?

### `MAT_RELIABILITY` — Tier C · Stream ✔
- **Output.**
  - `scalars.alpha`: Cronbach's α.
  - `scalars.alpha_standardized`.
  - `scalars.omega`: McDonald's ω, using a one-factor fit; stretch if `MAT_FACTOR` slips.
  - `vectors.item_total_r`: corrected item–total correlations.
  - `vectors.alpha_if_deleted`.
  - `primary`: the inter-item correlation matrix.
- **Params.**
  - `reverse: ["q3","q7"]`: reverse-score these items before computing.
  - `scale_min` / `scale_max`: the scale range needed for reversal.
- **Why.** This is the standard check before a battery is summed into a scale score. It is in nearly every attitude survey, and SPSS users expect it ("RELIABILITY" is a core SPSS procedure, and Pulse imports SPSS).

### `MAT_PCA` — Tier C · Stream ✔
- **Params.**
  - `on`: `correlation` (default) | `covariance`.
  - `components`: an integer k, or `"kaiser"` (eigenvalue > 1), or `{variance: 0.8}`.
- **Output.**
  - `primary`: loadings p×k.
  - `vectors.eigenvalues`, `explained_variance`, `cumulative`.
  - `scalars.kmo`, `bartlett_chisq`, `bartlett_p`.
- **Deterministic sign convention.** See `linalg`.
- **Why.** Turns many correlated measures into a few uncorrelated indices. It is also the explanatory front-end for `ATTR_PC_SCORE`.

### `MAT_FACTOR` — Tier S · Stream ✔ (fit is iterative at flush)
- **Method.** Exploratory factor analysis with `extraction`: `principal_axis` | `minres` | `ml`, and `rotation`: `none` | `varimax` | `promax` | `oblimin`.
- **Output.**
  - `primary`: rotated loadings.
  - `auxiliary.unrotated`.
  - `auxiliary.factor_correlation`: oblique rotations only.
  - `vectors.communalities`, `uniquenesses`.
  - `scalars`: fit indices.
- **Why stretch.** Iterative fitting, convergence edge cases (Heywood cases) and rotation all add design surface. PCA covers most of the need for v1.0.0.

### `MAT_COLLINEARITY` — Tier C · Stream ✔
- **Output.** `vectors.vif`, `vectors.tolerance`, `vectors.condition_indices`, and `auxiliary.variance_decomposition` (Belsley).
- **Why.** Run before a regression with many predictors. It pairs with `REG_OLS` and explains unstable coefficients.

### `MAT_DISTANCE` — Tier S · Stream ✔ (for centroid-based)
- **What it computes.** A distance matrix between **group centroids**: one vector mean per bucket of `params.between` (a grouper), using metric `euclidean` | `mahalanobis` | `correlation` | `cosine`.
- **Why.** Answers "how different are these segments / regions / stores on the whole profile?". The output is k×k, not n×n, so it scales.
- **Excluded.** Row-to-row distance matrices (n×n) are excluded deliberately, because they are unbounded output.

### `MAT_CANONICAL_CORRELATION` — Tier P
- **What it computes.** Two vectors in; canonical correlations, weights and loadings out.
- **Why defer.** Niche outside scientific users; the SVD plumbing from correspondence analysis makes it cheap later.

---

## Multivariate tests (`TEST_*` additions)

The `Test` request type gains an optional `vector` / `fields` input. The existing `Field` / `Field2` remain for univariate tests.

| Operator | Tier | Question | Notes |
|---|---|---|---|
| `TEST_HOTELLING_T2` | C | Do two groups differ on a whole mean vector? | Two co-moment accumulators (one per level of `params.by`); F-approximation; streamable. |
| `TEST_MANOVA` | C | Do k groups differ on a mean vector? | Wilks' Λ, Pillai's trace, Hotelling–Lawley, Roy; reports all four with F approximations; streamable (k accumulators). |
| `TEST_BOX_M` | S | Are covariance matrices equal across groups? (MANOVA assumption) | Streamable. |
| `TEST_MARDIA` | S | Multivariate normality (skewness/kurtosis) | Needs third and fourth multivariate moments; buffered. |
| `TEST_BARTLETT_SPHERICITY` | C | Is the correlation matrix the identity? (is factoring worthwhile) | Also emitted as a scalar by `MAT_PCA` / `MAT_FACTOR`; available standalone for parity. |

`TestResult` stays scalar: statistic, df, p-value. MANOVA's four statistics go in `Details`, with the chosen headline (Pillai, the most robust) as `Statistic`.

---

## Row-level attributes (`ATTR_*` additions)

Each needs a fitted matrix. Pulse already has the two-pass pattern in `ATTR_REG_FITTED` (fit then score), and these reuse it: **pass 1** accumulates co-moments, **pass 2** scores rows. They are therefore buffered, or two-scan under streaming, and must say so in `Streamable()`.

| Operator | Tier | Output per row | Typical use |
|---|---|---|---|
| `ATTR_MAHALANOBIS` | C | D² (and optional χ² p-value) from the centroid | survey fraud / straight-liners; ops anomaly flags; then `FILTER_RANGE` on the attribute |
| `ATTR_PC_SCORE` | C | score on component `params.component` (1-based) | turn 25 items into 3 indices that feed crosstabs |
| `ATTR_FACTOR_SCORE` | S | regression / Bartlett factor score | follows `MAT_FACTOR` |
| `ATTR_SCALE_SCORE` | C | mean or sum of a battery with `min_valid` and reverse-scoring | the everyday "compute scale score" step; single-pass and streamable (no fit needed) |
| `ATTR_PROFILE_SIMILARITY` | S | correlation / cosine of the row's vector to a reference profile (literal, lookup table, or a group centroid) | "how closely does this respondent match the ideal profile"; customer-to-segment fit |
| `ATTR_VEC_DISTANCE` | S | Euclidean / Mahalanobis distance to a reference vector | assignment to nearest segment centroid |

**Fitting scope.** `params.fit_on` is either `"filtered"` (default: the same rows being scored) or a named sub-population filter. The second lets a scoring model be fitted on, say, a "clean" base and applied to everyone.

---

## Vector aggregators (`AGG_*` additions)

| Operator | Tier | Output per group | Stream |
|---|---|---|---|
| `AGG_VEC_MEAN` | C | centroid as a labeled vector | ✔ |
| `AGG_VEC_SUM` | C | element-wise sum | ✔ |
| `AGG_VEC_SD` | S | element-wise SD | ✔ |

**Open design point.** An aggregator's value in `Response.Data` is a scalar today. A vector-valued aggregator either emits an array value (simplest, and JSON-friendly) or expands into `name[label]` columns. Proposal: emit an array plus the element labels in `Components`, and offer `params.expand: true` for tabular consumers. Crosstab cells stay scalar, so crosstab refuses vector aggregators with `PROCESSING_CONFIG`.

---

## Segmentation grouper — `GROUP_KMEANS` (Tier S)

- **Input.** A vector, `k`, a seed, `max_iter` and `standardize` (default true).
- **Algorithm.** k-means++ initialization with a seeded PRNG, then Lloyd iterations. Ties go to the lowest centroid index. Final labels are **renumbered by descending cluster size** (ties broken by centroid lexicographic order), so the output is stable across runs and shard orders.
- **Output.** A categorical key `cluster_1 … cluster_k`. Every aggregator, crosstab and overlay works on it unchanged. Centroids and within-SS go in `Components.Groupers` operator-specific keys.
- **Execution.** Buffered by nature. Rows hold their vector values, and memory is predictable at `n × p × 8` bytes, which predict reports. Sharded cohorts need a gather; that is documented, not hidden.
- **Out of scope.** Hierarchical clustering of rows (O(n²)). `OVERLAY_SERIATION` (04) provides hierarchical ordering at the result level instead, where n is small.

---

## Rolling windows (`WIN_*` additions) — Tier S

| Operator | Output | Use |
|---|---|---|
| `WIN_ROLLING_CORR` | rolling Pearson r between `Field` and `Field2` over `params.window` rows | ops / finance: does traffic still track conversions? |
| `WIN_ROLLING_BETA` | rolling OLS slope of `Field` on `Field2` | finance β, elasticity tracking |

Both reuse the two-variable co-moment with removal (a sliding Welford update). They are scalar per row, so they fit the existing window contract exactly.

---

## Regression additions

| Item | Tier | Notes |
|---|---|---|
| `RegressionResult.Vcov` | C | the coefficient covariance matrix as an additive `MatrixPayload` field; it is already computed (`InverseTo` in `ols_solver.go`) and thrown away. Enables custom contrasts and delta-method CIs downstream. |
| `RegressionResult.Correlation` | C | the coefficient correlation (Vcov scaled), useful for diagnosing collinearity |
| `REG_OLS` multi-Y (multivariate regression) | P | several dependent variables against the same X; shares the Gram matrix |
| `REG_PCR` / `REG_PLS` | P | regression on principal components / partial least squares; common in driver analysis, but deferred until PCA is stable |

---

## Synth additions — Tier S

- `synth.Spec` accepts a **declared correlation matrix** (from a `MAT_CORRELATION` result, or hand-authored). Generation already draws through Cholesky, so this is mostly an input path plus a nearest-correlation repair step.
- The fidelity report adds a **matrix distance** (Frobenius norm of the r-difference, and the maximum absolute cell difference) between the source profile and the synthetic output's correlation matrix.
- Per `synthetic-data.md`, every synth failure mode is silent, so both items need explicit parity tests before they are committed.
