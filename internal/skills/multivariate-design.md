---
name: multivariate-design
description: Choosing a multivariate operator on a battery of numeric fields — which matrix answers which question, what to do with missing data and weights, when a decomposition needs a repaired matrix, and where p-values stand. Topical design; per-operator params in the atomic op-mat-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [multivariate, battery, correlation matrix, partial correlation, reliability, PCA, collinearity, VIF, missing data, repair, rank correlation]
requires: [capability:matrices]
---

# Choosing a multivariate operator

Every operator here reads one vector (`vectors`, see `matrix-results`) and answers with a matrix plus vectors and scalars. Pick by the QUESTION, then set the missing-data mode.

## Which operator answers my question

| Question | Operator |
|---|---|
<!-- feature: MAT_COVARIANCE -->
| How do the fields co-vary, in their own units? | `MAT_COVARIANCE` |
<!-- /feature -->
<!-- feature: MAT_CORRELATION -->
| How strongly do fields move together (linear, or monotone with `params.method`)? | `MAT_CORRELATION` |
<!-- /feature -->
<!-- feature: MAT_PARTIAL_CORRELATION -->
| Is a link left once the other fields (or `control`) are held fixed? | `MAT_PARTIAL_CORRELATION` |
<!-- /feature -->
<!-- feature: MAT_RELIABILITY -->
| Do these items measure one scale (alpha, omega, which item to drop)? | `MAT_RELIABILITY` |
<!-- /feature -->
<!-- feature: MAT_PCA -->
| How many dimensions carry the variance; is the battery factorable (KMO, Bartlett)? | `MAT_PCA` |
<!-- /feature -->
<!-- feature: MAT_COLLINEARITY -->
| Are candidate predictors too redundant to fit together (VIF, condition index)? | `MAT_COLLINEARITY` |
<!-- /feature -->

<!-- feature: REG_OLS -->
How uncertain are the coefficients of a fitted model, and how do they co-move? That is not a matrix slot: set `"vcov": true` on the regression (`regression-inference`).
<!-- /feature -->

Rank correlation (`spearman`, `kendall`) suits ordinal data, outliers and monotone curves. It is buffered and serial, takes frequency weights only, and stays off the streaming path. Latent-factor analysis and rotation are not shipped: PCA is a variance summary, not a factor model.

## Missing data

- `listwise` (default): one row set for every cell; the matrix is PSD by construction. Prefer it when few rows drop; `max_drop_share` warns when many do.
- `pairwise`: each cell uses its own rows (`auxiliary.n` has the counts). It keeps more data but the matrix can fail to be PSD.
- Decompositions (partial correlation, PCA, collinearity, the omega of reliability) refuse a non-PSD pairwise input with `PULSE_MATRIX_NOT_PSD`. `params.repair: "nearest"` projects it to the nearest correlation matrix (Higham) and warns with the size of the change; a repair stopped at its cap also warns `PULSE_MATRIX_NOT_CONVERGED`. Reliability keeps alpha and nulls omega instead of refusing.
- A singular matrix is refused as `PULSE_MATRIX_SINGULAR` with `rank`, `condition_number` and `dependent_fields`: drop or combine the named fields, do not repair.
- Pairwise inference sizes use a conservative N* (the smallest pair); `PULSE_MATRIX_PAIRWISE_N_STAR` says so on PCA.

## Weights

<!-- feature: capability:weighting -->
Frequency weights work everywhere. Probability weights work on the co-moment operators (and use n_eff for sizes) but rank methods refuse them (`PULSE_WEIGHT_UNSUPPORTED`). Weight 0 counts toward `n` and adds no mass. See `weighting`.
<!-- /feature -->

## Significance

Matrices carry figures, not p-values. Rank and partial correlation have no raw p or interval yet, and nothing corrects for the many comparisons across the cells of a matrix: a grid of p = 0.05 cells hides false positives. Treat the values as descriptive until matrix p-values and their correction arrive.
<!-- feature: capability:multiplicity -->
Per-test correction on other operators: `multiplicity-correction`.
<!-- /feature -->

## Sanity checks

- Few rows against many members makes every figure unstable; read `components.matrices[i].n` and the per-pair counts before trusting a cell.
- A `null` cell is undefined (constant member, thin bucket), never 0.
- Grouped requests return one matrix per bucket; compare them as separate answers.

## See

- `matrix-results` — slot, encodings, refusals.
<!-- feature: MAT_PCA -->
- `op-mat-pca`
<!-- /feature -->
<!-- feature: MAT_RELIABILITY -->
- `op-mat-reliability`
<!-- /feature -->
<!-- feature: MAT_COLLINEARITY -->
- `op-mat-collinearity`
<!-- /feature -->
<!-- feature: REG_OLS -->
- `regression-inference` — coefficient covariance.
<!-- /feature -->
