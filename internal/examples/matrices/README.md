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

Run every example:

```
./internal/examples/matrices/run-all.sh
```
