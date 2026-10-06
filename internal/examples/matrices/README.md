# Matrices

Whole-set matrix operators (`MAT_*`) over virtual vectors
(`Request.Vectors`). Each spec in `matrices` returns one entry in
`Response.Matrices`.

| File | Operator | What it shows |
|---|---|---|
| `01_covariance.json` | `MAT_COVARIANCE` | A vector over three numeric columns and its sample covariance matrix + determinant. |

Run every example:

```
./internal/examples/matrices/run-all.sh
```
