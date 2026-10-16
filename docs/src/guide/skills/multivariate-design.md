```yaml
name: multivariate-design
description: Choosing a multivariate operator on a battery of numeric fields — which matrix answers which question, what to do with missing data and weights, when a decomposition needs a repaired matrix, and where p-values stand. Topical design; per-operator params in the atomic op-mat-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [multivariate, battery, correlation matrix, partial correlation, reliability, PCA, collinearity, VIF, missing data, repair, rank correlation]
requires: [capability:matrices]
```

# Choosing a multivariate operator

Every operator here reads one vector (`vectors`, see [`matrix-results`](matrix-results.md)) and answers with a matrix plus vectors and scalars. Pick by the QUESTION, then set the missing-data mode. For distance or similarity, declare the vector's `kind` first ([`vector-metrics`](vector-metrics.md)).

## Which operator answers my question

| Question | Operator |
|---|---|
| How do the fields co-vary, in their own units? | `MAT_COVARIANCE` |
| How strongly do fields move together (linear, or monotone with `params.method`)? | `MAT_CORRELATION` |
| Is a link left once the other fields (or `control`) are held fixed? | `MAT_PARTIAL_CORRELATION` |
| Do these items measure one scale (alpha, omega, which item to drop)? | `MAT_RELIABILITY` |
| How many dimensions carry the variance; is the battery factorable (KMO, Bartlett)? | `MAT_PCA` |
| Which hidden qualities do the items reflect (factor analysis, rotation)? | `MAT_FACTOR` |
| Does my CFA / path model fit? | `MAT_SEM` |
| Are candidate predictors too redundant to fit together (VIF, condition index)? | `MAT_COLLINEARITY` |
| Which rows are unusual across the fields as a whole? | `ATTR_MAHALANOBIS` (a row score) |
| Each row's score on a component (a summary column)? | `ATTR_PC_SCORE` (a row score) |
| Each row's score on a factor (a construct estimate)? | `ATTR_FACTOR_SCORE` (a row score) |
| Each row's score on a rating battery (mean or prorated sum, reverse-keyed items)? | `ATTR_SCALE_SCORE` (a row score) |
| How closely does each row match a target profile? | `ATTR_PROFILE_SIMILARITY` (a row score) |
| How far is each row from a target profile? | `ATTR_VEC_DISTANCE` (a row score) |
| How close is each row to its segment's centroid, or a saved population's? | `ATTR_PROFILE_SIMILARITY` with `reference: {centroid: {by}}` / `{population}` |
| Each group's average profile across the items, as one list? | `AGG_VEC_MEAN` |
| Each group's total per item, as one list? | `AGG_VEC_SUM` |
| How much each group disagrees on each item, as one list? | `AGG_VEC_SD` (sample sd per item) |
| Which segments do the rows fall into across the fields? | `GROUP_KMEANS` (a grouper) |
| How far apart are the segments' centres across the fields? | `MAT_DISTANCE` (`params.between` a grouper) |

How uncertain are the coefficients of a fitted model, and how do they co-move? That is not a matrix slot: set `"vcov": true` on the regression ([`regression-inference`](regression-inference.md)).

Rank correlation (`spearman`, `kendall`) suits ordinal data, outliers and monotone curves. It is buffered and serial, takes frequency weights only, and stays off the streaming path. PCA is a variance summary, not a factor model; `MAT_FACTOR` fits one (minres, principal axis or maximum likelihood with fit indices; orthogonal or oblique rotation); `MAT_PCA` takes an orthogonal `rotation` (varimax, …) for a simpler loading pattern.

## Missing data

- `listwise` (default): one row set for every cell; the matrix is PSD by construction. Prefer it when few rows drop; `max_drop_share` warns when many do.
- `pairwise`: each cell uses its own rows (`auxiliary.n` has the counts). It keeps more data but the matrix can fail to be PSD.
- Decompositions (partial correlation, PCA, factor analysis, collinearity, the omega of reliability) refuse a non-PSD pairwise input with `PULSE_MATRIX_NOT_PSD`. `params.repair: "nearest"` projects it to the nearest correlation matrix (Higham) and warns with the size of the change; a repair stopped at its cap also warns `PULSE_MATRIX_NOT_CONVERGED`. Reliability keeps alpha and nulls omega instead of refusing.
- A singular matrix is refused as `PULSE_MATRIX_SINGULAR` with `rank`, `condition_number` and `dependent_fields`: drop or combine the named fields, do not repair.
- Pairwise inference sizes use a conservative N* (the smallest pair); `PULSE_MATRIX_PAIRWISE_N_STAR` says so on PCA and factor analysis.

## Weights

Frequency weights work everywhere. Probability weights work on the co-moment operators (and use n_eff for sizes) but rank methods refuse them (`PULSE_WEIGHT_UNSUPPORTED`). Weight 0 counts toward `n` and adds no mass. See [`weighting`](weighting.md).

## Significance

Correlation (any method) and partial correlation give each pair a raw p and interval under `params.inference` ([`op-mat-correlation`](op-mat-correlation.md), [`op-mat-partial-correlation`](op-mat-partial-correlation.md)). Nothing corrects across a matrix's cells: a grid of p = 0.05 cells hides false positives, so read them as screening, not findings. To test whether whole mean profiles differ (one sample against a target, paired before / after sets, or two groups with pooled or unequal covariance), run `TEST_HOTELLING_T2` on the same `vector`; it is a test with a p-value. Across three or more groups run `TEST_MANOVA` (Pillai's trace headline; Wilks, Hotelling–Lawley and Roy beside it). Check their equal-covariance assumption with `TEST_BOX_M`. `TEST_BARTLETT_SPHERICITY` is PCA's Bartlett test as a standalone test. `TEST_MARDIA` checks the joint normality these tests assume.
Per-test correction on other operators: [`multiplicity-correction`](multiplicity-correction.md).

## Sanity checks

- Few rows against many members makes every figure unstable; read `components.matrices[i].n` and the per-pair counts before trusting a cell.
- A `null` cell is undefined (constant member, thin bucket), never 0.
- Grouped requests return one matrix per bucket; compare them as separate answers. Vector tests run per group; a singular or thin group gets a `null` result + warning, never an abort. Fitted score attributes fit cohort-wide by design, never per group.

## See

- [`matrix-results`](matrix-results.md) — slot, encodings, refusals.
- [`multivariate-outputs`](multivariate-outputs.md) — how the PCA, factor, distance, score and vector-test figures are laid out.
- [`vector-metrics`](vector-metrics.md) — vector `kind`, metric defaults and the unsuited-metric warning.
- [`op-mat-pca`](op-mat-pca.md)
- [`op-mat-factor`](op-mat-factor.md)
- [`op-mat-sem`](op-mat-sem.md)
- [`op-mat-distance`](op-mat-distance.md)
- [`op-mat-reliability`](op-mat-reliability.md)
- [`op-attr-scale-score`](op-attr-scale-score.md)
- [`op-agg-vec-mean`](op-agg-vec-mean.md)
- [`op-agg-vec-sd`](op-agg-vec-sd.md)
- [`op-mat-collinearity`](op-mat-collinearity.md)
- [`regression-inference`](regression-inference.md) — coefficient covariance.
