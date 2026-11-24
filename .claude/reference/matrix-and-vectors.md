# Matrix and vectors — the `linalg` core, virtual vectors and the matrix slot

**How to load.** This file is an INDEX: the package surface below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/matrix-and-vectors/` your change touches; find one with `grep -l '<identifier>' .claude/reference/matrix-and-vectors/*.md`. A pointer written as `matrix-and-vectors.md` (Section) resolves through the section map.

**Load it before touching `linalg/`, a caller of it (synth's correlator, the regression SPD path, `MAT_SEM`), `internal/processing/block_merge.go`, the parallel-decode segment split, or any `MAT_*` / vector surface.** Package layout and import gates: `.claude/reference/architecture.md` (Public vs internal, Surface guards). Engine wiring of the blocked merge: `.claude/reference/execution-modes.md` (Blocked merge). Embedder guide: `docs/src/library/linalg.md`. Operator prose: the `op-mat-*` skills and `multivariate-design`.

## Package surface

`linalg/` is public and frozen-additive (`TestPublicAPIGolden`; the golden is the symbol list). It holds Pulse-owned value types (`Matrix` dense row-major, `Sym` packed upper triangle, `Vec`) and routine groups with different bit contracts:

| Group | Examples | Backend | Bit contract |
|---|---|---|---|
| Reference SPD kernels | `Cholesky`, `CholeskyRidge`, `SolveSPD`, `InverseSPD` | pure Go, FMA-free | identical bits on every architecture |
| Reference general solve | `Solve`, `FactorLU` → `LUFactor` | pure Go, FMA-free; the singular decision rides gonum singular values | values identical on every architecture; refusal portable except within an ulp of the rank tolerance |
| Co-moments | `CoMoment`, `ListwiseState`, `MergeBlockSize`, `MergeTree` | pure Go, FMA-free | identical bits on every architecture |
| Decompositions | `SymEigen`, `SVD`, `QR`, `Rank`, `ConditionNumber` | gonum, canonicalised | decisions (order, sign, rank) portable; values may differ in the last ulps across architectures |
| Gonum-order SPD path | `FactorSPD` → `SPDFactor`, `Mul`, `ConditionTolerance` | pure Go, FMA-free port of gonum v0.17.0's amd64 operation order | identical bits on every architecture; equal to the raw gonum call on amd64 |

**No gonum type appears on the exported surface**: gonum is pre-1.0 and the surface is frozen. `TestLinalgSurfaceNamesNoGonum` asserts the golden's `linalg` section carries no `gonum` token; `TestLinalgImportBoundary` keeps the package a leaf over the standard library, gonum, Pulse's `errors` and the stdlib-only `internal/gonumorder`.

## Section map

| Section | Slice |
|---|---|
| Metric registry — `linalg/metric`; Backend policy — why two backends; Tolerances, order and sign; Errors; `CoMoment` weight and missing semantics; Blocked merge-tree contract; Parity tolerance rationale; Cross-architecture detail; Open edges | `matrix-and-vectors/linalg.md` |
| Virtual vectors — `Request.Vectors`: shape, native vector fields, vector fields in scalar slots, kind, order, standardize, computed members, member types, labels, order of checks, where it runs, expression binding and slots, unreferenced warning, hashing, codes | `matrix-and-vectors/vectors.md` |
| Virtual vectors (continued): vector-input tests, `ATTR_SCALE_SCORE`, reference comparison, centroid / population / stored-record references, fitted attributes, prebuilt fitted state, `GROUP_KMEANS` | `matrix-and-vectors/vector-operators.md` |
| Virtual vectors — vector aggregators and expand: `AGG_VEC_*`, `params.element`, `params.expand`, overlays scoring them | `matrix-and-vectors/vector-aggregators.md` |
| Matrix slot — `Request.Matrices` / `Response.Matrices`: wire, records encoding, resolution, engine, paths, grouped, refusals, `top_pairs`, overlays on a matrix result, precision, `return` exclusion, `params.fit`, self-description, predict, choosing among the operators | `matrix-and-vectors/matrix-slot.md` |
| Matrix slot (continued): `MAT_COVARIANCE`, `MAT_CORRELATION` (+ rank methods), `MAT_PARTIAL_CORRELATION`, inference and multiplicity, `PULSE_MATRIX_NOT_CONVERGED`, missing modes and warnings | `matrix-and-vectors/matrix-correlation.md` |
| Matrix slot (continued): PSD guard and repair, one-factor minres, `MAT_RELIABILITY`, `MAT_PCA`, rotation engine, `MAT_COLLINEARITY` | `matrix-and-vectors/matrix-decomposition.md` |
| Matrix slot (continued): `MAT_FACTOR`, `MAT_DISTANCE` | `matrix-and-vectors/matrix-factor-distance.md` |
| Matrix slot (continued): `MAT_SEM` — the semopy clone, options, resolution, outputs, refusals, singular fallback, weights; the lavaan-convention features (mean structure, multi-group, invariance, predict / feature profiles, defined parameters, bootstrap, Wald level) | `matrix-and-vectors/matrix-sem.md` |
| `MAT_SEM` group selection — derived groups, `params.group_levels`, row admission, `ATTR_SEM_SCORE` on a derived group | `matrix-and-vectors/sem-groups.md` |
| `MAT_SEM` reference fixtures — semopy fixtures and release, same-shape parity | `matrix-and-vectors/matrix-sem-fixtures.md` |
| `MAT_SEM` ordinal two-step statistics — thresholds, polychoric / polyserial / Pearson, Gamma, the DWLS / WLSMV estimator, wiring, groups / bootstrap, bits, oracles | `matrix-and-vectors/sem-ordinal.md` |
| `ATTR_SEM_SCORE` — per-row SEM latent scores: arithmetic, scale, multi-group, ordinal (EBM) scores, inline versus bound binding, bit-equality, kinds, failures, weight, features | `matrix-and-vectors/sem-scores.md` |
| Regression vcov — `RegressionSpec.Vcov`, dispersion, penalized inverse, modifiers, robust / panel / two-way covariance, marginal effects | `matrix-and-vectors/regression-vcov.md` |
