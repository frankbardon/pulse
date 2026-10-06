---
id: U24
slug: matrix-operators
title: "Analysts get the core multivariate toolkit: rank correlations, partial correlations, reliability, PCA, collinearity"
track: Vector & matrix
size: L
status: not-started
depends_on: [U16]
soft_depends_on: [U13]
blocks: [U25, U28, U31]
todo_items: [124, 125, 126, 127, 128, 129, 130, 172]
branch: matrix-operators
---

# U24 — matrix-operators

**Outcome:** Analysts get the core multivariate toolkit: rank correlations, partial correlations, reliability, PCA, collinearity.

**Track:** Vector & matrix · **Size:** L · **Depends on:** [U16](U16-matrix-result.md) · **Soft:** [U13](U13-multiplicity.md) · **Unblocks:** [U25](U25-multivariate-tests-segmentation.md), [U28](U28-matrix-overlays.md), [U31](U31-guidance-guides.md)

## Summary

Add `MAT_CORRELATION` Spearman/Kendall, `MAT_PARTIAL_CORRELATION`, `MAT_RELIABILITY`, `MAT_PCA` (with KMO/Bartlett), `MAT_COLLINEARITY`, plus `RegressionResult.Vcov`/`.Correlation` and the `multivariate-design.md` topical skill. Each ships with Purpose/Interpretation, weight support and examples.

## References

**Theme documents (read before starting):**
- [vector-matrix 03 — Multivariate statistics](../v1.0.0-vector-matrix/03-multivariate-statistics.md) — MAT_* operators (C tier)
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E3

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#124** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_CORRELATION` Spearman / Kendall
- [ ] **#125** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_PARTIAL_CORRELATION`
- [ ] **#126** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_RELIABILITY` (α, standardized α, ω, item-total, α-if-deleted, reverse scoring)
- [ ] **#127** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_PCA` (loadings, eigenvalues, explained variance, KMO, Bartlett)
- [ ] **#128** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_COLLINEARITY` (VIF, tolerance, condition indices)
- [ ] **#129** (10. Vector & matrix — operators › E3 — Core matrix operators) `RegressionResult.Vcov` and `.Correlation`
- [ ] **#130** (10. Vector & matrix — operators › E3 — Core matrix operators) Topical skill `multivariate-design.md`

## Scope

**In scope**
- Five operators + regression vcov
- Topical skill
- Purpose/Interpretation/Since/deps for each
- Weighting where co-moment based

**Out of scope**
- Multivariate tests and fitted attributes (U25)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(matrix-operators/E<n>-S<m>): …`; close each epic with `milestone(matrix-operators/E<n>): vertical slice complete — <epic title>`.

### E1 — Correlation family complete
- S1: Spearman/Kendall (buffered) with parity vs `TEST_SPEARMAN_R`/`TEST_KENDALL_TAU`
- S2: `MAT_PARTIAL_CORRELATION`

### E2 — Scales and dimensions
- S1: `MAT_RELIABILITY` (reverse scoring, α-if-deleted)
- S2: `MAT_PCA` (sign convention, KMO, Bartlett)

### E3 — Collinearity and model covariance
- S1: `MAT_COLLINEARITY`
- S2: `RegressionResult.Vcov`/`.Correlation`
- S3: `multivariate-design.md`

## Acceptance criteria

- [ ] Rank correlations match the pairwise tests byte-for-byte
- [ ] Cronbach's α and PCA match reference values from a published dataset fixture
- [ ] PCA output is deterministic across platforms (sign convention)
- [ ] Each operator passes the atomic skill, coverage, example-tag and purpose gates
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAllMatrixOps`, `TestManifestMatrixOpsComplete`, `TestEveryOperatorHasAnExampleTag`, guidance gates

## Update Demand companions

- `skills/op-mat-*.md`; `descriptor/capabilities_matrix.go`; examples
- `regression-modeling.md` (vcov)

## Handed on from U16

- **Higham `repair: "nearest"`** (the repair half of TODO #172; theme 05 X2). U16 only DETECTS: pairwise matrices warn `PULSE_MATRIX_NOT_PSD` (`vectors.Matrix.PSDRisk()`, diagonal-scaled reference Cholesky, tolerance 1e-10). Decompositions (PCA, partial correlation, collinearity) refuse a non-PSD input unless `params.repair: "nearest"`, reported as a warning with the Frobenius adjustment.
- **`PULSE_MATRIX_SINGULAR` details.** Add `rank` / `condition_number` on the reference Cholesky and `FactorSPD` paths (today `pivot`, `reason`, `n`); no routine populates `rank`.
- **Jacobi vs gonum portability.** The matrix slot is FMA-free end to end; gonum eigen / SVD results differ in the last ulps between amd64 and arm64. Decide the PCA sign convention and the determinism gate with that in mind.
- **Reuse.** Matrices are `types.MatrixValues`; operators register in `matrixFinalizers` and must join `MatrixType.Mergeable()` when co-moment based (rank-based ones are not). Contract: `.claude/reference/matrix-and-vectors.md`.


## Human inputs & decisions

- Statistics reviewer for Purpose/Interpretation of the new operators
