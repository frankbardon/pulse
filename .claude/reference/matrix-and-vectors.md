# Matrix and vectors — the `linalg` core, its backend policy and the blocked merge (U15)

**How to load.** This file is an INDEX: the package surface below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/matrix-and-vectors/` your change touches. A pointer written as `matrix-and-vectors.md` (Section) resolves through the section map.

CLAUDE.md names `linalg` in the public package list and points here. This file is the long form of the numerical contract every matrix and vector feature builds on. **Load it before touching `linalg/`, a caller of it (synth's correlator, the regression SPD path), `internal/processing/block_merge.go`, the parallel-decode segment split, or any future `MAT_*` / vector surface.** Package layout and the import gates: `.claude/reference/architecture.md` (Public vs internal, Surface guards). Engine wiring of the blocked merge: `.claude/reference/execution-modes.md` (Blocked merge). Embedder-facing guide: `docs/src/library/linalg.md`.

U15 shipped no request slot, response slot, operator, manifest block or skill. The only user-visible addition is two error codes in the manifest error list. Synth and regression output did not move by one bit.

## Package surface

`linalg/` is public and frozen-additive (`TestPublicAPIGolden`). It holds Pulse-owned value types (`Matrix` dense row-major, `Sym` packed upper triangle, `Vec`) and three groups of routines:

| Group | Routines | Backend | Bit contract |
|---|---|---|---|
| Reference SPD kernels | `Cholesky`, `CholeskyRidge` (+ `RidgeSchedule`, `DefaultRidgeSchedule`), `SolveSPD`, `InverseSPD` | pure Go, FMA-free | identical bits on every architecture |
| Reference general solve (U28) | `Solve`, `FactorLU` → `LUFactor` (`Solve`, `SolveMatrix`, `Inverse`, `ConditionNumber`, `N`) | pure Go, FMA-free; the singular decision rides gonum singular values | values identical bits on every architecture; refusal portable except within an ulp of the rank tolerance |
| Co-moments | `CoMoment` (`NewCoMoment`, `Add`, `Merge`, `Clone`, `N`, `W`, `NEff`, `NWeightInvalid`, `PairN`, `PairNMass`, `PairW`, `PairW2`, `PairNEff`, `PairM2`, `Mean`, `Cov`, `Corr`, `ListwiseState`; modes `Listwise` / `Pairwise`), `ListwiseState` + `NewCoMomentFromListwise`, `MergeBlockSize`, `MergeTree` | pure Go, FMA-free | identical bits on every architecture |
| Decompositions | `SymEigen`, `SVD`, `QR`, `Rank`, `ConditionNumber` | gonum, canonicalised | decisions (order, sign, rank) portable; values may differ in the last ulps across architectures |
| Gonum SPD path | `FactorSPD` → `SPDFactor` (`Solve`, `Inverse`, `ConditionNumber`, `N`), `Mul`, `ConditionTolerance` | thin gonum wrappers | equal to the raw gonum call bit for bit on ONE machine; not across architectures |

No gonum type appears on the exported surface: gonum is pre-1.0 and the surface is frozen. `TestLinalgSurfaceNamesNoGonum` (`internal/apigolden`) asserts the golden's `linalg` section carries no `gonum` token; `TestLinalgImportBoundary` (`linalg/boundary_test.go`) keeps the package a leaf over the standard library, gonum and Pulse's `errors`.

## Section map

| Section (pre-split heading) | Slice |
|---|---|
| Metric registry — `linalg/metric`; Backend policy — why two backends; Tolerances, order and sign; Errors; `CoMoment` weight and missing semantics; Blocked merge-tree contract; Parity tolerance rationale; Open edges (handed on) | `matrix-and-vectors/linalg.md` |
| Virtual vectors — `Request.Vectors`: shape, native vector fields, vector fields in scalar slots, kind, order, member types, labels, order of checks, where it runs, expression binding, unreferenced warning, hashing, codes | `matrix-and-vectors/vectors.md` |
| Virtual vectors (continued): vector-input tests, `ATTR_SCALE_SCORE`, reference comparison, vector-input (fitted) attributes, vector-input groupers | `matrix-and-vectors/vector-operators.md` |
| Virtual vectors — vector aggregators and expand: `AGG_VEC_MEAN` / `AGG_VEC_SUM` / `AGG_VEC_SD`, `params.element`, `params.expand` | `matrix-and-vectors/vector-aggregators.md` |
| Matrix slot — `Request.Matrices` / `Response.Matrices`: wire, resolution, engine, paths, grouped, refusals, `top_pairs`, overlays on a matrix result, precision, `return` exclusion, `params.fit`, self-description, predict, choosing among the operators | `matrix-and-vectors/matrix-slot.md` |
| Matrix slot (continued): `MAT_COVARIANCE`, `MAT_CORRELATION` (+ rank methods), `MAT_PARTIAL_CORRELATION`, inference, `PULSE_MATRIX_NOT_CONVERGED`, missing modes and warnings | `matrix-and-vectors/matrix-correlation.md` |
| Matrix slot (continued): PSD guard and repair, one-factor minres, `MAT_RELIABILITY`, `MAT_PCA`, rotation engine, `MAT_COLLINEARITY` | `matrix-and-vectors/matrix-decomposition.md` |
| Matrix slot (continued): `MAT_FACTOR`, `MAT_DISTANCE` | `matrix-and-vectors/matrix-factor-distance.md` |
| Matrix slot (continued): `MAT_SEM` — the semopy clone (specification, SLSQP port, SEs, fit indices), options (`solver`, `information` rank-2), resolution, outputs, refusals, weights; the lavaan-convention features (mean structure, multi-group, invariance, predict / profiles, defined parameters, bootstrap) | `matrix-and-vectors/matrix-sem.md` |
| Matrix slot (continued): `MAT_SEM` reference fixtures — the semopy fixtures and release, same-shape parity | `matrix-and-vectors/matrix-sem-fixtures.md` |
| Matrix slot (continued): `ATTR_SEM_SCORE` — per-row SEM latent scores: arithmetic and scale, multi-group levels, inline (two-pass) versus bound (row-local) binding, bit-equality with a compose or saved fit, kinds, failures, weight, features | `matrix-and-vectors/sem-scores.md` |
| Regression vcov — `RegressionSpec.Vcov` (incl. robust covariance) | `matrix-and-vectors/regression-vcov.md` |
