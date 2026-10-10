# Vectors and Matrices

A **vector** names a battery of numeric fields once; a **matrix** slot
turns a vector into a result in which every member is compared with
every member. Six operators ship: `MAT_COVARIANCE`,
`MAT_CORRELATION` (Pearson by default; Spearman or Kendall tau-b under
`params.method`), `MAT_PARTIAL_CORRELATION`, `MAT_RELIABILITY`,
`MAT_PCA` and `MAT_COLLINEARITY`. All are additive: a request without
`vectors` / `matrices` is byte-identical to before, and `format_version`
stays `"1.1"`. The slots are gated by the `capability:matrices` feature.

```json
{
  "cohort": {"filename": "training_data.pulse"},
  "vectors": [{"name": "money", "fields": ["price", "income", "label"]}],
  "matrices": [
    {"type": "MAT_CORRELATION", "vector": "money",
     "params": {"missing": "pairwise", "summary": {"top_pairs": 3}}}
  ]
}
```

## Vectors

`vectors: [{name, fields | pattern, labels?, coerce?}]`, exactly one of
`fields` / `pattern`. A literal field keeps the caller's order; a glob
(`q*`) expands in place in schema order; `pattern` is an unanchored
regexp in schema order. Members are integer and float fields;
`packed_bool` needs `coerce: "binary"`; categorical, `set_*`, `date`,
`datetime` and `decimal128` are refused with `PULSE_VECTOR_MEMBER_TYPE`.
Other codes: `PULSE_VECTOR_EMPTY`, `_DUPLICATE`, `_LABELS_MISMATCH`,
`_UNKNOWN`, `_INVALID`; a vector no matrix names warns
`PULSE_VECTOR_UNREFERENCED`. `Pulse.Predict` echoes the resolved members
as `resolved_vectors`.

## Matrices

`matrices: [{name?, type, vector | fields, params?, weight?, encoding?}]`
returns `Response.Matrices`, one `types.MatrixResult` per spec in
request order:

| Key | Meaning |
|---|---|
| `primary` | the matrix, a `types.MatrixValues` `{kind, encoding, row_keys, column_keys, labels?, values}` |
| `auxiliary` | pairwise only: `n`, each pair's row count, same shape |
| `vectors` | `top_pairs` (`[{row, col, r, n}]`) for `MAT_CORRELATION` with `summary.top_pairs` |
| `scalars` | `determinant`, null unless the matrix is positive definite |
| `warnings` | `PULSE_MATRIX_*` data-quality findings |
| `group_key`, `group_header` | set on a per-bucket result |

`encoding` is `full` (default) or `upper` (row r carries the p - r cells
from the diagonal). Undefined cells are `null`, keys kept.

## Missing data and weights

- `missing: "listwise"` (default) drops a row with any null member;
  `"pairwise"` uses, per cell, the rows where both members are present.
  A pairwise matrix of three or more members may fail to be positive
  semi-definite (`PULSE_MATRIX_NOT_PSD`: detection only on the
  covariance and correlation operators, fatal on a decomposition
  operator — see Partial correlation).
- `max_drop_share` (listwise) warns `PULSE_MATRIX_LISTWISE_HEAVY_DROP`.
- `PULSE_MATRIX_INSUFFICIENT_N` and `PULSE_MATRIX_ZERO_VARIANCE` flag thin
  or constant members. A constant member nulls its correlation row and
  column; the covariance keeps an exact 0.
- Weights follow [Row Weighting](weighting.md); a weight-0 row counts
  toward `n` and adds no mass.

## Rank correlation

`MAT_CORRELATION` `params.method`: `pearson` (default), `spearman` or
`kendall` (tau-b). A rank method ranks each member first (pairwise:
each pair re-ranked over its own rows, as R's
`cor(use = "pairwise.complete.obs")`), and each cell equals
`TEST_SPEARMAN_R` / `TEST_KENDALL_TAU` over the pair's rows. It keeps
every admitted row until finalize, so the request runs buffered and
serially (predict: `streamable` and `mergeable` false,
`row_buffer_bytes`); it takes frequency weights only (a probability
weight is `PULSE_WEIGHT_UNSUPPORTED`) and emits no p-values.

## Partial correlation

`MAT_PARTIAL_CORRELATION` reads each pair's Pearson correlation with
other fields held fixed, off the precision matrix of the members'
correlation (the same co-moments as `MAT_CORRELATION`, so it streams,
merges and takes both weight kinds). `params.control` is `"all"`
(default: every pair controls for every other member, as
`ppcor::pcor`) or a list of numeric fields: each output pair then
controls for exactly those (`ppcor::pcor.test(x, y, Z)`), a listed
member leaves the output axis, and a listed field outside the members
joins the fold and its missing-data mode (projection and
`Components.Matrices` count it). No p-values, no scalars; pairwise adds
`auxiliary.n`. If any input correlation is undefined every cell is
`null`.

It is a **decomposition** operator, so its input must be usable: a
pairwise input that is not positive semi-definite is refused with the
fatal `PULSE_MATRIX_NOT_PSD` (predict flags the risk as
`pairwise_psd_risk`) unless `params.repair: "nearest"`, which replaces
it by the nearest correlation matrix — Higham's alternating projections
with Dykstra's correction, `Matrix::nearPD(corr = TRUE)`'s settings
(at most 100 iterations, convergence 1e-7, eigenvalue floor 1e-8 of the
largest) — and keeps the code as a warning carrying
`frobenius_adjustment`, `iterations` and `converged`. A member or
control that is a linear mix of the others is `PULSE_MATRIX_SINGULAR`
with `rank`, `condition_number` and `dependent_fields`.

## Scale reliability

`MAT_RELIABILITY` checks a battery of items before it is summed into a
score. Its `primary` is the inter-item correlation matrix; `scalars`
carry Cronbach's `alpha` (on the covariance, `psych::alpha` raw_alpha),
`alpha_standardized` (from the mean inter-item r), `mean_inter_item_r`
and McDonald's `omega`; `vectors` carry, per item in axis order, the
corrected `item_total_r` (the item against the sum of the others),
`alpha_if_deleted`, `item_mean` and `item_sd`.

Reverse-worded items are named in `params.reverse` together with the
battery's `scale_min` / `scale_max`: each value becomes
`scale_min + scale_max − x` before the fold, so the operator streams and
merges like `MAT_CORRELATION` and takes both weight kinds. A reverse
list without the range, or any item value outside a declared range, is
`PROCESSING_CONFIG` — values are never clamped and the range is never
read off the data.

`omega` is `(Σλ)² / ((Σλ)² + Σψ)` from a one-factor minimum-residual fit
of the inter-item correlation (`psych::fa(nfactors = 1, fm = "minres")`);
`components.matrices[i].operator` reports the fit's `iterations` and
`converged`. It is `null` with a warning when the battery has 2 items
(`PULSE_MATRIX_NOT_IDENTIFIED`), when the fit is a Heywood case
(`PULSE_MATRIX_HEYWOOD`), or when a pairwise table is not positive
semi-definite and `params.repair` is not `"nearest"`
(`PULSE_MATRIX_NOT_PSD` as a warning — alpha needs no such input and is
always reported). Alpha is read against George & Mallery's (2003) bands;
omega is not banded.

## Principal components

`MAT_PCA` decomposes the members' correlation (`params.on`
`"correlation"`, the default) or covariance (`"covariance"`). Its
`primary` is the loadings — eigenvector × √eigenvalue — as a
**rectangular** matrix (`kind: "rectangular"`): `row_keys` are the
members, `column_keys` the kept components `PC1` … `PCk`, always written
in full. `auxiliary.eigenvectors` has the same shape. `vectors` carry
every `eigenvalue` (descending), its `explained_variance` share and the
`cumulative` share, the `communalities` (each member's summed squared
loadings over the kept components) and `kmo_msa`; `scalars` carry the
overall `kmo`, Bartlett's sphericity test (`bartlett_chisq`,
`bartlett_df`, `bartlett_p`) and `components_retained`.

`params.components` chooses k: an integer (at most the member count),
`"kaiser"` (every eigenvalue above 1 — the default on a correlation,
refused on a covariance, whose eigenvalues are in the members' units),
or `{"variance": share}` (the fewest leading components whose cumulative
share reaches it). On a covariance it is required. Sign and order follow
`linalg.SymEigen`: eigenvalues descending, each eigenvector's
largest-magnitude entry positive — portable across architectures, with
values agreeing to the eigensolver's last digits.

KMO (`psych::KMO`) and Bartlett (`psych::cortest.bartlett`) always read
the correlation. Bartlett's sample size follows the weighted-inference
rule: the rows listwise, Σw under frequency weights, Kish n_eff under
probability weights, and under pairwise deletion the smallest pair's
(`PULSE_MATRIX_PAIRWISE_N_STAR` says so). A singular correlation nulls
KMO and Bartlett with a `PULSE_MATRIX_SINGULAR` warning, the components
still reported. Like the partial correlation, a non-PSD pairwise input
is refused unless `params.repair` is `"nearest"`. KMO is read against
Kaiser's (1974) bands. No rotation (factor analysis and rotation are a
later operator).

## Collinearity

`MAT_COLLINEARITY` checks candidate regression predictors before a
model is fitted. Its members are the predictors only — there is no
response, so the figures are the same whatever outcome is later
modelled. `vectors.vif` is each predictor's variance inflation factor,
the diagonal of the inverse correlation matrix (`car::vif` on an `lm`
over the same predictors), and `vectors.tolerance` its reciprocal; the
`primary` is the predictors' correlation matrix and `scalars.max_vif`
the largest VIF.

Belsley's diagnostics (`perturb::colldiag`) ride beside them:
`vectors.condition_indices` (ascending, the first exactly 1),
`scalars.condition_number` (the largest) and
`auxiliary.variance_decomposition`, a **rectangular** matrix with a row
per variable and a column per dimension `D1` … `Dq` in condition-index
order — each row is that variable's coefficient variance split across
the dimensions, summing to 1. By default they are computed on the
scaled, **uncentered** predictors with an intercept column (Belsley's
recommendation, which also shows collinearity with the intercept; the
first row is `(intercept)`), rebuilt from the slot's co-moments as
W(Σ + μμᵀ), so the operator stays streamable and mergeable.
`params.center: true` computes them on the centered predictors (their
correlation) instead.

An exactly collinear set has no inverse and is refused with
`PULSE_MATRIX_SINGULAR`, whose `dependent_fields` name the members in
the dependency. Like the partial correlation, a non-PSD pairwise input
is refused unless `params.repair` is `"nearest"`. Weighted under
frequency and probability weights (the figures are scale-free, so the
two kinds agree). Neither VIF nor the condition index is banded: 5 or 10
for VIF and 30 for the condition index are rules of thumb only
(O'Brien 2007).

## Groups, cost and refusals

With `groups`, a matrix is computed per bucket, in `Response.Data` order
(`sort` included). `Pulse.Predict` reports `matrices[]`: `shape`,
`accumulator_bytes`, `streamable`, `mergeable`, `row_buffer_bytes` (a
rank method), `pairwise_psd_risk`, and for a grouped request
`bucket_basis`, `estimated_buckets`, `estimated_cells` and
`estimated_bytes` (an upper bound; omitted when the bucket count depends
on the data). `estimated_bytes` is the run's co-moment state: merge
blocks (`ceil(records / 4096)`, counted per shard for an archive) ×
buckets × `accumulator_bytes`, plus a rank method's row buffer. Matrix dimension is capped by
`Options.Limits.MaxMatrixDim`, and the opt-in
`Options.Limits.MaxEstimatedMemory` counts `estimated_bytes` in the
memory estimate it checks before any record is read.

Refused identically by predict and the run: matrices with `joins` or on a
chain stage after the first (`PULSE_MATRIX_UNSUPPORTED_SOURCE`), and with
`crosstab` (`PULSE_MATRIX_HOST_CONFLICT`).

## Determinism

Serial, parallel-decode and sharded runs return bit-identical matrices
for a cohort file; a multi-shard archive matches its single-file twin
within rounding. A correlation cell agrees with the corresponding
`TEST_PEARSON_R` within `1e-12 + 1e-12 * |r|`.

Contract: `.claude/reference/matrix-and-vectors.md`; payload shapes:
[Payload Schema](../contract/payload-schema.md); agent guide: the
`matrix-results` skill; kernels: [Linear Algebra](linalg.md).
