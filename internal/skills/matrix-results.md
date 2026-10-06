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
- `MAT_CORRELATION` — Pearson r, no p-values (run a test per pair for those); `params.summary.top_pairs: k` lists the k strongest pairs.
<!-- /feature -->

Reading a result: `primary` is the matrix (`row_keys` = members, `labels` only when you set them); `scalars.determinant` is null unless the matrix is positive definite; `warnings` lists data-quality findings. **Undefined cells are `null`, never NaN or 0.**

`encoding`: `full` (default, p rows of p cells) or `upper` (row r holds the p − r cells from the diagonal, `values[r][k]` = cell (r, r + k)) — about half the bytes; both are symmetric.

## 3. Missing data and weights

`params.missing`: `listwise` (default, drop a row if any member is null) or `pairwise` (each cell uses the rows where both members are present; `auxiliary.n` gives each pair's row count).

- Pairwise matrices of three or more members can fail to be positive semi-definite (`PULSE_MATRIX_NOT_PSD`); do not feed them to anything that needs PSD.
- `max_drop_share` (listwise only) warns `PULSE_MATRIX_LISTWISE_HEAVY_DROP` when more than that share of rows was dropped.
- `PULSE_MATRIX_INSUFFICIENT_N` (under two rows or no weight mass) and `PULSE_MATRIX_ZERO_VARIANCE` (a constant member: its correlation row and column are null; covariance keeps an exact 0).
- Weights follow `weighting`: frequency and probability both fold into a weighted covariance. A row of weight 0 counts toward `n` but adds no mass.

## 4. Per group

A request with `groups` returns one `MatrixResult` per non-empty bucket and spec, each with `group_key` / `group_header`, in the same order as `Response.data` (`sort` included). A thin bucket is emitted with nulls and `PULSE_MATRIX_INSUFFICIENT_N`. Each bucket equals the ungrouped matrix over exactly its rows. Only the first `groups` entry is executed.

## 5. Components and cost

`components.matrices[i]` is the floor: `n`, `n_null`, `n_listwise_dropped`, pairwise `min_pair_n` / `max_pair_n`, weighted `sum_weights` / `n_eff` / `n_weight_invalid` (`response-components`).

`pulse predict` returns `matrices[]` before any row is read: `shape`, `axis_keys`, `accumulator_bytes`, `pairwise_psd_risk` (false guarantees no NOT_PSD warning), `streamable`. For a grouped request it adds:

| Key | Meaning |
|---|---|
| `bucket_basis` | where the bucket count comes from: `ungrouped`, `dictionary`, `boolean`, `include`, `quantile_bins`, `unknown` |
| `estimated_buckets` | upper bound on buckets (omitted when `unknown`) |
| `estimated_cells` | buckets × p² |
| `estimated_bytes` | buckets × `accumulator_bytes` |

A range or date grouper is `unknown`: the three figures are omitted, never guessed. They are a report, not a guard — large p with many buckets is your call.

## 6. What is refused

- `matrices` with `joins`, or on a chain stage after the first: `PULSE_MATRIX_UNSUPPORTED_SOURCE`.
- `matrices` with `crosstab`: `PULSE_MATRIX_HOST_CONFLICT`.
- Predict and the run refuse identically, before any record is read.
- Matrices are not streamed row by row: they arrive at the final flush. `ProcessStream` rows carry no matrices.

Serial, parallel-decode and sharded runs return bit-identical matrices on one cohort file; a multi-shard archive matches its single-file twin within rounding only.

## See

`request-envelope` · `weighting` · `response-components` · `grouper-design`
