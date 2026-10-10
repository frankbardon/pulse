---
name: matrix-results
description: Vectors and matrices on a Request — naming a battery of numeric fields once, getting a covariance or correlation matrix back, the encodings, missing-data modes, warnings, per-group matrices, predict's cost estimate and what is refused.
type: guide
kind: design
applies_to: process, compose, predict
covers: [vectors, matrices, covariance, correlation, pairwise deletion, listwise deletion, upper triangle, top_pairs, determinant]
requires: [capability:matrices]
---
# Vectors and matrices

A **vector** names numeric fields once; a **matrix** slot turns a vector into a square result (every member against every member). Both are additive: no `vectors` / `matrices` is byte-identical to before, and `format_version` stays `"1.1"`.

## 1. Name the vector

`vectors: [{name, fields | pattern, labels?, coerce?}]` — exactly one of `fields` / `pattern`.

- A literal `fields` entry keeps the caller's position; a glob (`q*`) expands in place in schema order; `pattern` is an unanchored regexp in schema order.
- Members: integer and float types. `packed_bool` only with `coerce: "binary"`. Categorical, `set_*`, `date`, `datetime`, `decimal128` are refused (`PULSE_VECTOR_MEMBER_TYPE`).
- `labels`: one per member, else `PULSE_VECTOR_LABELS_MISMATCH`. A vector no matrix uses warns `PULSE_VECTOR_UNREFERENCED`.
- `pulse predict` echoes `resolved_vectors` — check the members before running.

## 2. Ask for the matrix

`matrices: [{name?, type, vector | fields, params?, weight?, encoding?}]`; the answer is `Response.matrices[]`, one `MatrixResult` per spec in request order.

<!-- feature: MAT_COVARIANCE -->
- `MAT_COVARIANCE` — covariance; `params.ddof` 0 or 1 (default 1).
<!-- /feature -->
<!-- feature: MAT_CORRELATION -->
- `MAT_CORRELATION` — Pearson r, or `params.method` `spearman` / `kendall` (frequency weights only); no p-values; `params.summary.top_pairs: k` lists the k strongest pairs.
<!-- /feature -->
<!-- feature: MAT_PARTIAL_CORRELATION -->
- `MAT_PARTIAL_CORRELATION` — partial r, `params.control` `"all"` or fields held fixed; non-PSD input fatal unless `params.repair: "nearest"` (a repair stopped at its iteration cap also warns `PULSE_MATRIX_NOT_CONVERGED`).
<!-- /feature -->
<!-- feature: MAT_COLLINEARITY -->
- `MAT_COLLINEARITY` — VIF, tolerance, Belsley condition indices + variance decomposition; `params.center`.
<!-- /feature -->
<!-- feature: MAT_PCA -->
- `MAT_PCA` — loadings (rectangular p × k), eigenvalues, KMO, Bartlett; `params.components` k / `kaiser` / `{variance}`.
<!-- /feature -->
<!-- feature: MAT_RELIABILITY -->
- `MAT_RELIABILITY` — alpha, omega, item diagnostics; `params.reverse` + `scale_min` / `scale_max` flip reverse-keyed items.
<!-- /feature -->

Reading a result: `primary` is the matrix (`row_keys` = members, `labels` only when you set them); `scalars.determinant` is null unless the matrix is positive definite; `warnings` lists data-quality findings. **Undefined cells are `null`, never NaN or 0.**

`encoding`: `full` (default, p rows of p cells) or `upper` (row r holds the p − r cells from the diagonal, `values[r][k]` = cell (r, r + k)) — about half the bytes.

## 3. Missing data and weights

`params.missing`: `listwise` (default, drop a row if any member is null) or `pairwise` (each cell uses the rows where both members are present; `auxiliary.n` gives each pair's row count).

- Pairwise matrices of three or more members can fail to be positive semi-definite (`PULSE_MATRIX_NOT_PSD`); do not feed them to anything that needs PSD.
- `max_drop_share` (listwise only) warns `PULSE_MATRIX_LISTWISE_HEAVY_DROP` when more than that share of rows was dropped.
- `PULSE_MATRIX_INSUFFICIENT_N` (under two rows or no weight mass) and `PULSE_MATRIX_ZERO_VARIANCE` (a constant member: its correlation row and column are null; covariance keeps an exact 0).
- Weights follow `weighting`: frequency and probability both fold into a weighted covariance. A row of weight 0 counts toward `n` but adds no mass.

## 4. Per group

A request with `groups` returns one `MatrixResult` per non-empty bucket and spec, each with `group_key` / `group_header`, in the same order as `Response.data` (`sort` included). A thin bucket is emitted with nulls and `PULSE_MATRIX_INSUFFICIENT_N`. Each bucket equals the ungrouped matrix over exactly its rows. Only the first `groups` entry is executed.

## 5. Components and cost

`components.matrices[i]` is the floor: `n`, `n_null`, `n_listwise_dropped`, pairwise `min_pair_n` / `max_pair_n`<!-- feature: capability:weighting -->, weighted `sum_weights` / `n_eff` / `n_weight_invalid`<!-- /feature --> (`response-components`).

`pulse predict` returns `matrices[]` before any row is read: `shape`, `axis_keys`, `accumulator_bytes`, `pairwise_psd_risk` (false guarantees no NOT_PSD warning), `streamable`. For a grouped request it adds:

| Key | Meaning |
|---|---|
| `bucket_basis` | where the bucket count comes from: `ungrouped`, `dictionary`, `boolean`, `include`, `quantile_bins`, `unknown` |
| `estimated_buckets` | upper bound on buckets (omitted when `unknown`) |
| `estimated_cells` | buckets × p² |
| `estimated_bytes` | merge blocks (`ceil(records / 4096)`, per shard) × buckets × `accumulator_bytes` — the run's state |

A range or date grouper is `unknown`: the three figures are omitted, never guessed. They are a report; the host's `max_estimated_memory` limit (opt-in) counts `estimated_bytes` in its memory estimate.

## 6. What is refused

- `matrices` with `joins`, or on a chain stage after the first: `PULSE_MATRIX_UNSUPPORTED_SOURCE`.
- `matrices` with `crosstab`: `PULSE_MATRIX_HOST_CONFLICT`.
- Matrices are not streamed row by row: they arrive at the final flush. `ProcessStream` rows carry no matrices.

Serial, parallel-decode and sharded runs return bit-identical matrices on one cohort file; a multi-shard archive matches its single-file twin within rounding only.

Streamability is per spec, not per type: a spec whose params need every row at once (a rank method) is buffered — predict `matrices[].streamable` / `mergeable` false — so the whole request runs buffered (`ProcessStream` too) and serially.

## See

`request-envelope` · `weighting` · `response-components` · `grouper-design`
