# Vectors and Matrices

A **vector** names a battery of numeric fields once; a **matrix** slot
turns a vector into a square result in which every member is compared
with every member. Two operators ship: `MAT_COVARIANCE` and
`MAT_CORRELATION` (Pearson). Both are additive: a request without
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
  semi-definite (`PULSE_MATRIX_NOT_PSD`, detection only).
- `max_drop_share` (listwise) warns `PULSE_MATRIX_LISTWISE_HEAVY_DROP`.
- `PULSE_MATRIX_INSUFFICIENT_N` and `PULSE_MATRIX_ZERO_VARIANCE` flag thin
  or constant members. A constant member nulls its correlation row and
  column; the covariance keeps an exact 0.
- Weights follow [Row Weighting](weighting.md); a weight-0 row counts
  toward `n` and adds no mass.

## Groups, cost and refusals

With `groups`, a matrix is computed per bucket, in `Response.Data` order
(`sort` included). `Pulse.Predict` reports `matrices[]`: `shape`,
`accumulator_bytes`, `pairwise_psd_risk`, and for a grouped request
`bucket_basis`, `estimated_buckets`, `estimated_cells` and
`estimated_bytes` (an upper bound; omitted when the bucket count depends
on the data). Nothing caps them yet.

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
