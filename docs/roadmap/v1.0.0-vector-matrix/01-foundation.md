# 01 — Foundation: linear algebra core, vectors, field types

Everything in documents 03 and 04 sits on five foundation pieces. They are listed in build order.

---

## F1. `linalg/` — one linear-algebra package (Committed)

### Problem
Pulse has two Cholesky implementations: gonum in `processing/regression/ols_solver.go` and a hand-written one in `synth/copula.go` (`cholesky` / `tryCholesky`, around line 901). It has no eigen, SVD or QR anywhere. Every new feature would otherwise reach into gonum ad hoc, and nothing would enforce consistent numerical policy (tolerances, PSD repair, sign conventions).

### Proposal
A new leaf package `linalg/` (stdlib + gonum only, importing nothing else from Pulse) that owns:

| Function family | Used by |
|---|---|
| `Cholesky`, `SolveSPD`, `InverseSPD` | regression, Mahalanobis, Hotelling, synth draws |
| `SymEigen` (sorted descending, sign-normalized) | PCA, factor, steady state, condition index |
| `SVD` (thin) | correspondence analysis, canonical correlation |
| `QR` | collinearity diagnostics, least squares fallback |
| `NearestCorrelation` (Higham 2002) | repair of pairwise-deletion correlation matrices that are not PSD |
| `ConditionNumber`, `Rank(tol)` | diagnostics and coded warnings |
| `Rotate{Varimax,Promax}` | factor analysis |
| `PowerIterate`, `StationaryDistribution` | Markov overlay |
| `IPF` | raking overlay |

Policies live here, once:
- **Tolerances.** Relative rank tolerance `max(p) · ε · σ_max`; one constant, documented.
- **Sign convention.** Each eigenvector / singular vector is flipped so that its largest-magnitude component is positive. Without this, gonum may return either sign, and goldens flake across platforms.
- **Ordering.** Eigenvalues descending; ties broken by original variable order.

### Boundary rule
`descriptor/` may import `linalg/` (it is pure maths and executes no request), which lets predict check things like "is p ≤ the configured cap". A new import-boundary gate in the style of `TestTemplatePackage_ImportBoundary` would stop `linalg/` from importing `processing/`, `service/` or `descriptor/`.

### Migration
- `synth/copula.go` and `synth/residual_draw.go` move onto `linalg.Cholesky`. The synth fidelity goldens must stay byte-identical. That is the acceptance test: if they move, the hand-rolled version had different rounding and the change needs a documented reason.
- `processing/regression` moves onto `linalg` for its solve and inverse. gonum stays the backend.

---

## F2. Mergeable weighted co-moment accumulator (Committed)

### Problem
`TEST_PEARSON_R` already keeps a streaming two-variable cross-product. Generalizing it to `p` variables gives the sufficient statistic for most of document 03.

### Proposal
`processing/comoment` would provide an accumulator holding `{W, mean[p], M2[p×p] (upper triangle)}`, with optional per-pair counts for pairwise deletion.

- **Update.** A weighted Welford / West update per row, costing O(p²).
- **Merge.** The Chan–Golub–LeVeque pairwise combine: `M2 = M2a + M2b + δδᵀ · Wa·Wb/W`. It is exact, so this accumulator is **mergeable**, and every consumer inherits streaming, `ShardWorkers`, `DecodeWorkers` and ProcessChain eligibility.
- **Missing data modes.**
  - `listwise` (default): one `W` and one triangle; a row contributes only if all `p` values are present.
  - `pairwise`: per-pair `W_ij` and per-pair means; costs about 3× the memory. The result may not be positive semi-definite (PSD) — see 05.
- **Memory.** `p(p+1)/2` float64s, or ×3 for pairwise. At p = 256 that is 33K entries, about 260 KB, which is trivial. Predict reports this as a cost estimate.

### Why this matters
One accumulator then produces covariance, correlation, partial correlation, PCA, VIF, Cronbach's α (from the item covariance), Mahalanobis (via its inverse), Hotelling T² (two accumulators) and MANOVA (k accumulators). It is written and tested once.

---

## F3. Virtual vectors — `Request.Vectors` (Committed)

### Problem
Existing cohorts store a battery as sibling columns (`q12_1 … q12_15`). Listing 15 names in every operator is noisy, error-prone and expensive for LLM callers in tokens.

### Proposal
Add a new additive request slot that names column lists once per request:

```jsonc
{
  "vectors": [
    { "name": "brand_image", "fields": ["q12_1", "q12_2", "q12_3", "q12_4"] },
    { "name": "satisfaction", "pattern": "^sat_\\d+$" },          // regex, schema order
    { "name": "spend_mix", "fields": ["spend_*"], "labels": ["Food","Fuel","Travel"] }
  ],
  "matrices": [ { "type": "MAT_CORRELATION", "vector": "brand_image" } ]
}
```

- **Members.** A member is any numeric field type. A categorical member is refused, and so is `packed_bool` unless `coerce: "binary"` is set (0/1 items are legitimate in a reliability analysis).
- **Resolution.** `fields` (with glob) XOR `pattern`. Names resolve against the schema in **schema order**, so the result is deterministic. An empty resolution is a coded error.
- **Scope.** A vector's `name` lives in the request namespace. Every `MAT_*`, `ATTR_*` vector operator, multivariate `TEST_*`, and the expr functions in F5 accept `vector:` in place of `fields:`.
- **Hashing.** The vector definition goes into the request hash (`types/hash.go`). Two requests that resolve to the same field list hash the same — normalize before hashing.
- **Projection.** `processing.NeededFields` expands a vector to its members, so projected decode keeps working.
- **Predict.** Predict resolves vectors and echoes the resolved member list in `PredictResult`, so an LLM can see what a pattern matched.

This piece needs no file-format change, and it is what makes v1.0.0 useful on every existing cohort.

---

## F4. Native vector field types — `vec_f32`, `vec_f64` (Committed)

### Problem
Virtual vectors cover existing data. New imports of grid batteries, time profiles and compositional splits deserve a type that keeps the values together and carries their dimension labels in the schema.

### Proposal

| Type | Type byte | Element | Stride | Dim range |
|---|---|---|---|---|
| `vec_f32` | 20 | float32 | 4 × dim | 2…1024 |
| `vec_f64` | 21 | float64 | 8 × dim | 2…1024 |

Fixed dimension per field. The fixed-width row model does not change, because the stride is still a pure function of the schema.

**Wire.** The field descriptor stays unchanged; the dimension cannot fit in it. The dimension and optional element labels go into a new **schema extension section** (`0x02` tagged-section block, tag `2` = `VECTORS`, REQUIRED). That means:
- Following the existing rule that the version is a function of schema content, a cohort with a vector field is written as `0x02`. Ungrouped cohorts without vectors stay byte-identical `0x01`.
- **No `0x03` is needed.** This is exactly the extensibility path `byte-layout.md` describes ("how later additions stay readable or refusable WITHOUT a `0x03`").
- An older binary refuses such a cohort loudly: it sees an unknown type byte, and also an unknown REQUIRED section.

The section carries, per vector field: `u16 field_index`, `u16 dim`, `u8 flags` (bit 0 = has element labels; bit 1 = compositional, meaning values sum to 1), then optional `dim` length-prefixed UTF-8 labels.

**Nulls.** Nullability is whole-vector, using the existing per-record bitmap. Element-level missingness is NaN inside the vector, and an operator option chooses whether NaN is skipped or poisons the result. Document this split clearly: null means "the respondent did not see the battery", NaN means "skipped one item".

**Integer / ordinal grids.** v1 covers float elements only. A `vec_u8` (Likert 1–7 grids at a quarter of the width) is a *stretch* item. Its value is mostly storage size, and it can follow once the f32/f64 path is proven.

**Import.** `pulse import` needs a way to fold sibling columns into a vector field. The option would be `--vector NAME:COL1,COL2,...` or `--vector NAME:/regex/`, mirroring the existing `--group KEY:MEMBER,...` flag. NDJSON / JSON arrays and Arrow `FixedSizeList` can map directly. Parquet `LIST` with a constant length maps directly; any other length is refused.

**Export.** A vector field expands back to `name_1 … name_N` (or `name_<label>`) columns for CSV / SPSS / Excel. Arrow and Parquet export keep it as a fixed-size list.

**Interaction with other features.**
- *Parent groups:* a vector field may be a group member; its whole stride is copied verbatim, like any member.
- *Shards:* the dimension is part of strict cohesion. Element labels are description-tolerant.
- *Sidecar point-lookup index:* a vector cannot be a key field.
- *Smart defaults:* none. A bare vector field in an aggregation slot is refused with a pointer to `AGG_VEC_MEAN`.
- *Dedup / widen:* operate on whole strides.

**Accessor API.** `encoding.ReadFieldValue` returns `uint64` and cannot carry a vector. A vector gets its own accessor (`ReadVector(dst []float64)`), and the scalar API refuses it with `ENCODING_TYPE_MISMATCH`. This follows the precedent of `set_u128` / `set_u256`.

**Update Demand.** This is the heaviest Update Demand row: `byte-layout.md`, `skills/type-vec-f32.md`, `skills/type-vec-f64.md`, `cohort-schema-design.md`, the CLAUDE.md "Byte-layout invariants" section (the field-type count goes 20 → 22), the adding-field-type recipe and the payload schema enums.

---

## F5. expr-lang vector functions (Committed)

### Problem
`ATTR_FORMULA` and `FILTER_EXPRESSION` can read several columns of one row, but have no vector vocabulary. Per the op skill, they also lack `sqrt` / `log`.

### Proposal
Bind each vector (virtual or native) as a `[]float64` identifier in the expr environment, and add a small built-in function set:

| Function | Meaning |
|---|---|
| `v[i]` | element access (0-based, matching expr-lang slices) |
| `len(v)` | dimension |
| `vsum(v)`, `vmean(v)`, `vmin(v)`, `vmax(v)`, `vsd(v)` | row-wise reductions (NaN-skipping) |
| `vcount(v)` | non-NaN element count, for "min valid items" rules |
| `dot(a, b)`, `norm(v)`, `dist(a, b)`, `cosine(a, b)` | geometry |
| `argmax(v)`, `argmin(v)` | index of the extreme value (e.g. "top-rated brand") |
| `vcenter(v)`, `vzscore(v)`, `vnormalize(v, "l1"\|"l2")` | vector-returning, usable inside an expression (ipsatization, shares) — see [07](07-similarity-and-distance.md) S3 |
| `jaccard(a, b)`, `dice(a, b)`, `hamming(a, b)`, `overlap(a, b)` | set / binary similarity over `set_*` masks (popcount) — see [07](07-similarity-and-distance.md) S4 |
| `sqrt`, `log`, `exp`, `abs`, `pow` | scalar maths, closing a gap the op skill already notes |

Reference vectors could be supplied as literals (`[1,0,0]`) or through a named `LookupTable`.

**Why committed.** It is cheap (pure functions, no new execution path), and it covers a large share of everyday needs with no new operator. Examples are "mean of the battery if at least 6 of 8 items are answered" and "flag rows whose rating spread is zero" (straight-lining).

**Caution.** This sits on the boundary with request templating. `$var` / `{{}}` substitute before decode; vector functions run inside expr-lang at execution time. The two stay non-interoperating, as CLAUDE.md requires.
