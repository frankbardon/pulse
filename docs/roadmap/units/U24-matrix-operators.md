---
id: U24
slug: matrix-operators
title: "Analysts get the core multivariate toolkit: rank correlations, partial correlations, reliability, PCA, collinearity"
track: Vector & matrix
size: L
status: done
depends_on: [U16]
soft_depends_on: [U13]
blocks: [U25, U28, U31]
todo_items: [124, 125, 126, 127, 128, 129, 130, 172, 258]
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

- [x] **#124** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_CORRELATION` Spearman / Kendall
- [x] **#125** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_PARTIAL_CORRELATION`
- [x] **#126** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_RELIABILITY` (α, standardized α, ω, item-total, α-if-deleted, reverse scoring)
- [x] **#127** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_PCA` (loadings, eigenvalues, explained variance, KMO, Bartlett)
- [x] **#128** (10. Vector & matrix — operators › E3 — Core matrix operators) `MAT_COLLINEARITY` (VIF, tolerance, condition indices)
- [x] **#129** (10. Vector & matrix — operators › E3 — Core matrix operators) `RegressionResult.Vcov` and `.Correlation`
- [x] **#130** (10. Vector & matrix — operators › E3 — Core matrix operators) Topical skill `multivariate-design.md`
- [x] **#258** (7. Guided analysis › Follow-ups from U23) `MAT_RELIABILITY` (Cronbach's alpha) needs `Purpose.KnownAs` aliases ("cronbach's alpha") when the operator lands, so the U23 synonym tier finds it

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

- [ ] Rank correlations match the pairwise tests (`TEST_SPEARMAN_R` / `TEST_KENDALL_TAU`) cell for cell: they run the tests' own kernels, so the figures are identical on one machine and match R `cor(method = ...)` to 1e-12
- [ ] Cronbach's α and PCA match reference values from a published dataset fixture (`datasets::attitude`, R `psych` / `eigen`, 1e-10 / 1e-9)
- [ ] PCA output follows one sign convention (largest-magnitude component positive) and holds the R-oracle tolerance gate (1e-9) on amd64 and arm64; bits are identical on one machine, not across architectures (gonum eigensolver)
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

## Landed

Merged without a release (rolls into v1.0.0); `format_version` stays `"1.1"`. `MAT_CORRELATION` `params.method` `spearman` / `kendall`; `MAT_PARTIAL_CORRELATION`; `MAT_RELIABILITY` (α, standardized α, ω, item diagnostics, reverse scoring); `MAT_PCA` (loadings in a new `rectangular` matrix kind, KMO, Bartlett); `MAT_COLLINEARITY`; the shared PSD guard with Higham `repair: "nearest"` and the fatal `PULSE_MATRIX_NOT_PSD` on decompositions; the one-factor minres solver; coded warnings `PULSE_MATRIX_NOT_CONVERGED` / `_HEYWOOD` / `_NOT_IDENTIFIED` / `_PAIRWISE_N_STAR`; opt-in `RegressionSpec.Vcov` → `RegressionResult.Vcov` / `.Correlation`; the `multivariate-design` and `regression-inference` skills. Every output is pinned to an R oracle (see the [review record](../reviews/U24-matrix-operators-review.md)); the record carries the open owner calls and the items for the U33 reviewer (#206).

## Handed on

| Item | Owner |
|---|---|
| Raw p-values and intervals on Spearman / Kendall `MAT_CORRELATION` | [U28](U28-matrix-overlays.md) #261 |
| Partial-correlation statistic and p-value | [U28](U28-matrix-overlays.md) #265 |
| `MAT_FACTOR` (multi-factor minres, reusing the one-factor solver and warnings) | [U25](U25-multivariate-tests-segmentation.md) #262 |
| Factor rotation (varimax, promax), including PCA varimax | [U25](U25-multivariate-tests-segmentation.md) #263 |
| `factor` glossary link and a third `measure_construct` declarer; clears both exemption-ledger entries | [U25](U25-multivariate-tests-segmentation.md) #264 |
| Oracle gaps: pairwise PCA case, weighted pairwise cases, `mv_vcov.json` at converged weights (GLM tolerance 1e-6 → ~1e-10) | [U36](U36-reference-oracles.md) #266 |
| `REG_GLM` gamma dispersion (Pulse 1, R estimated) | [U36](U36-reference-oracles.md) #271 |
| Predict `row_buffer_bytes` under fan-out groupers; `MatrixStateBytes` and the buffered row store | [U35](U35-predict-runtime-parity.md) #267 |
| `op-mat-*` soft-budget overruns (`op-mat-correlation` 1970 B against 1200) | [U32](U32-docs-audit.md) #268 |
| Rating-battery demo cohort for the reliability / PCA examples | [U31](U31-guidance-guides.md) #269 |
| Collinearity non-PD Belsley warning-path test; per-pair Σw² for probability-weighted pairwise `item_sd` | [U30](U30-matrix-extensions-hardening.md) #270 |
| Statistics review: findings MR-01 to MR-08, omega form, Heywood handling | [U33](U33-v1-release.md) #206 |
| Owner calls: bare `spearman` / `kendall` aliases; `read-only-analyst.json` edited in place; non-converged minres keeps ω; `MAT_COLLINEARITY` wire shape; vcov refused for resample / selection; converged-weights `mv_vcov.json` | maintainer, recorded in the [review record](../reviews/U24-matrix-operators-review.md) |

## Human inputs & decisions

- Statistics reviewer for Purpose/Interpretation of the new operators
