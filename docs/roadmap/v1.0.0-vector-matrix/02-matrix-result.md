# 02 — Matrix result shape

**Decision (interview):** add a new typed matrix result slot. Do not shoehorn matrices into `Response.Crosstab` or flatten them into `Response.Data`.

---

## R1. `Request.Matrices` / `Response.Matrices` (Committed)

The request and response each gain one additive `omitempty` slot that mirrors the other. This follows the pattern `Regressions` and `Tests` already use.

```jsonc
// request
{
  "vectors":  [{ "name": "img", "pattern": "^img_\\d+$" }],
  "groups":   [{ "field": "segment" }],                 // optional: one matrix per group
  "matrices": [
    { "name": "img_corr", "type": "MAT_CORRELATION", "vector": "img",
      "params": { "method": "pearson", "missing": "pairwise" }, "weight": "wt" },
    { "name": "img_pca",  "type": "MAT_PCA", "vector": "img",
      "params": { "components": 3, "on": "correlation" } }
  ]
}
```

```jsonc
// response (abridged)
"matrices": [
  {
    "name": "img_corr",
    "type": "MAT_CORRELATION",
    "group_key": null,
    "primary": { /* MatrixPayload, see R2 */ },
    "auxiliary": { "n": { /* MatrixPayload: pairwise N */ } },
    "vectors": { },
    "scalars": { "determinant": 0.0123, "condition_number": 41.7 },
    "warnings": []
  },
  {
    "name": "img_pca",
    "type": "MAT_PCA",
    "primary":   { /* loadings p × k */ },
    "vectors":   { "eigenvalues": [...], "explained_variance": [...], "cumulative": [...] },
    "scalars":   { "kmo": 0.81 }
  }
]
```

### `MatrixResult` fields

| Field | Meaning |
|---|---|
| `name`, `type` | echo of the spec, for correlation with the request |
| `group_key` | present when `Request.Groups` is set: one `MatrixResult` per group bucket, in grouper order |
| `primary` | the headline matrix |
| `auxiliary` | named secondary matrices (pairwise `n`, p-values, rotated vs unrotated loadings, residual correlations) |
| `vectors` | named labeled 1-D results (eigenvalues, communalities, α-if-item-deleted) |
| `scalars` | named scalar results (α, KMO, Bartlett χ², determinant) |
| `warnings` | per-matrix coded warnings, mirroring `OverlayLayer.Warnings` |

Every operator **declares its own `primary` / `auxiliary` / `vectors` / `scalars` keys in the manifest** (R4). The shape is self-describing, so an LLM never has to guess what `auxiliary.p` means.

---

## R2. Generalized `MatrixPayload`

The crosstab already has `MatrixPayload {RowHeader, ColumnHeader, RowKeys, ColumnKeys, Cells[][]MatrixCell, RowMargins, ColumnMargins, GrandTotal, CellLabel}`. Reuse it rather than invent a parallel type, with these additive changes:

- `Symmetric bool`. When true, the matrix may be emitted **upper-triangle only** (`encoding: "upper"`) and `Cells[r]` has `p − r` entries. This halves the payload for correlation and covariance, which matters for MCP token budgets at p = 50 (1,275 cells instead of 2,500). The default is full, to keep consumers simple. `Request` / CLI / MCP opt-in: `"matrix_encoding": "upper"`.
- `Kind` — `"square_symmetric" | "square" | "rectangular"`.
- Axis keys for variable-indexed matrices use the **field name**, plus the label from `LabelBinding` or the vector's element labels. The `AxisKey` shape needs no change.
- `MatrixCell.Value` stays scalar for matrix results. Rich per-cell payloads (n, p, CI) go in `auxiliary` matrices of the same shape, never in the cell. This keeps the "one number per cell" invariant that overlays rely on.

Matrices are **not** crosstabs: the margins are absent by default. An operator may populate them when it has a meaning; reliability's item-total correlations are one example.

---

## R3. Components

`Response.Components` gains `Matrices []MatrixComponents`, one entry per matrix slot:

- Universal floor `{n, n_null, n_listwise_dropped}`. Under pairwise deletion it also carries the `min_pair_n` / `max_pair_n` range.
- `Operator map[string]any`, keyed by each operator's `ComponentSchema`. Examples are the accumulator's `W`, the iteration count for factor or k-means, and whether a convergence flag was set.
- Mergeability class: `Mergeable` for co-moment-based operators, `None` for rank-based ones and k-means.

This triggers the `Response.Components` Update Demand row: `response-components.md`, CLAUDE.md "Output Format Contract", `skills/response-components.md` and each atomic skill's `## Components` section.

---

## R4. Manifest, predict and payload schema

- **Manifest.** Add a `components.matrices` slice (one entry per `MAT_*`, with `Inputs`, `Params`, `OutputKeys{primary, auxiliary[], vectors[], scalars[]}`, `Streamable`, `Mergeable`) and a `Matrix` capability block (max dimension, supported encodings, missing modes). Declarations go in a new `descriptor/capabilities_matrix.go`.
- **Predict.** Predict resolves vectors, checks member types, and reports per matrix: `shape: [p, p]` or `[p, k]`, axis labels, estimated accumulator bytes, `streamable`, and whether pairwise-deletion PSD repair may be needed. It can know all of this without reading a record, which keeps the no-execute contract.
- **Payload schema.** `MatrixSpec`, `VectorSpec` and `MatrixResult` reach `BuildPayloadSchema` through reflection. `AllMatrixTypes()` becomes a registry-injected enum. The golden is regenerated, and the `$id` version stays `"1.1"`.
- **Streaming.** Mergeable matrices are emitted at terminal flush only; no partial matrices go out mid-stream. A "running correlation" in a streaming chunk is a v1.1 consideration.

---

## R5. LLM / harness ergonomics

Matrices are the first Pulse output whose size grows quadratically with the request. Rules for a harness-friendly shape:

1. **Prefer `upper` encoding and rounding.** Add `params.precision` (significant digits, default unlimited). For an LLM, three digits on a correlation is plenty, and cutting digits significantly reduces payload size.
2. **Top-k summaries.** Add an optional `summary: {top_pairs: 10}` on correlation-family matrices. It emits the strongest `|r|` pairs as a short ranked list in `vectors.top_pairs`, which is often all an agent needs.
3. **Dimension caps.** `Options.Limits.MaxMatrixDim`, default **2,048** under the "high defaults" decision (see [embedder operations 01](../v1.0.0-embedder-operations/01-resource-limits.md)). Beyond the cap the request fails at predict with `PULSE_LIMIT_EXCEEDED` instead of returning a quadratic payload. Output volume below the cap is managed by the developer through [response shaping](../v1.0.0-response-shaping/00-design.md).
4. **CLI.** `pulse api process --json` emits the typed envelope. Human output renders a labeled grid, abbreviated for large p, plus the scalar block.
