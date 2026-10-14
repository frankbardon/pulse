---
id: U30
slug: matrix-extensions-hardening
title: "Embedders can add their own matrix operators, and the matrix stack is proven at scale"
track: Vector & matrix
size: M
status: not-started
depends_on: [U25, U28, U29]
soft_depends_on: []
blocks: [U32]
todo_items: [156, 157, 158, 270, 402, 403, 406, 408, 411, 415, 420, 423, 424, 425, 426, 449]
branch: matrix-extensions-hardening
---

# U30 — matrix-extensions-hardening

**Outcome:** Embedders can add their own matrix operators, and the matrix stack is proven at scale.

**Track:** Vector & matrix · **Size:** M · **Depends on:** [U25](U25-multivariate-tests-segmentation.md), [U28](U28-matrix-overlays.md), [U29](U29-vector-field-types.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

`MatrixOpRegistration` and the `MAT` naming-policy namespace with probe validation; benchmarks at p = 50/256 over 1M/10M rows (peak heap, shard scaling); examples-library entries for every new operator.

## References

**Theme documents (read before starting):**
- [vector-matrix 05 — Cross-cutting concerns](../v1.0.0-vector-matrix/05-cross-cutting.md) — X7 Extension points
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E10 (committed part)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#156** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) `MatrixOpRegistration` and the `MAT` naming-policy namespace; probe validation
- [ ] **#157** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) Benchmarks: p = 50 / 256 at 1M and 10M rows; peak heap; shard scaling
- [ ] **#158** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) Examples-library entries for every new operator
- [ ] **#270** (10. Vector & matrix — operators › Follow-ups from U24) Matrix hardening: a per-pair Σw² for probability-weighted pairwise `item_sd` in the co-moment state
- [ ] **#402** (10. Vector & matrix — operators › Follow-ups from U25) Compose co-moment fits: fit a vector battery's co-moment state once in a compose `fits` entry and let several slots (`MAT_*`, `ATTR_MAHALANOBIS`, `ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE`) reuse it, as regressions already do
- [ ] **#403** (10. Vector & matrix — operators › Follow-ups from U25) Named populations: save a fitted vector population (mean, covariance, loadings) under a name and score other cohorts against it, instead of re-fitting with `fit_on` per request
- [x] **#406** (10. Vector & matrix — operators › Follow-ups from U25) More GPA rotations for `MAT_FACTOR` / `MAT_PCA`: quartimax, geomin, oblimax, and the `GPArotation` criteria beyond varimax, promax and oblimin, on the shared rotation engine; done in U30 E6-S2: `quartimax`, `entropy`, `geomin_orthogonal` (orthogonal; `MAT_PCA` / `ATTR_PC_SCORE` too) and `geomin` (oblique; `params.epsilon`), refused oblique on PCA with `rotation_oblique_unsupported`; `oblimax` not offered (degenerate under GPFoblq on most inputs, and quartimax when orthogonal) — rationale in `.claude/reference/matrix-and-vectors/matrix-decomposition.md` (Rotation engine)
- [x] **#408** (10. Vector & matrix — operators › Follow-ups from U25) One singularity rule across the multivariate stack: the tests decide singular by `gonum` `SymEigen` against `RankTolerance`, while `MAT_PCA` is singular only when `InverseSPD` fails; align them and add the borderline case to the oracle
- [x] **#411** (10. Vector & matrix — operators › Follow-ups from U25) `MAT_DISTANCE` `between` refuses date-family groupers (the time-zone pass does not reach `params`) and extension groupers; resolve the zone for the date family and give extension groupers a centroid hook, or document the refusal as final
- [x] **#415** (10. Vector & matrix — operators › Follow-ups from U25) `ATTR_FACTOR_SCORE`: a refusal (`PULSE_MATRIX_NOT_IDENTIFIED` / `_HEYWOOD`) drops the fit's not-converged note; carry it on the refusal warning
- [ ] **#449** (10. Vector & matrix — operators › Follow-ups from U28) The `flows` intent needs ≥3 declaring operators; `OVERLAY_MARKOV` is its only declarer. Add genuine flows operators and drop the `intentDeclarerExemptions` entry

## Scope

**In scope**
- Extension registration + probe
- Benchmarks
- Examples completeness

**Out of scope**
- Stretch operators

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(matrix-extensions-hardening/E<n>-S<m>): …`; close each epic with `milestone(matrix-extensions-hardening/E<n>): vertical slice complete — <epic title>`.

### E1 — Embedders can extend matrices
- S1: `MatrixOpRegistration` + naming policy + probe + manifest/predict parity

### E2 — Proven at scale and documented by example
- S1: benchmarks + recorded baselines
- S2: examples for every new operator

## Acceptance criteria

- [ ] An extension `MAT_ACME_X_Y` registers, appears in manifest/predict/MCP identically to built-ins, and is probe-validated
- [ ] Benchmarks are committed with peak-heap figures
- [ ] `TestEveryOperatorHasAnExampleTag` passes for all new operators
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestExtensions_*` extended
- `TestEveryOperatorHasAnExampleTag`

## Update Demand companions

- `docs/src/internals/extension-points.md`; CLAUDE.md naming-policy regex

## Handed on from U16

- **`BlockMerger` through `extend`.** The blocked merge opt-in (`internal/processing/block_merge.go`) is internal; `MatrixOpRegistration` (#156) must expose it, or an embedder `MAT_*`-style operator is bit-unstable across worker counts. Today `MAT_` is NOT in the extension naming regex and `matrixFinalizers` has no extension registry (`.claude/reference/matrix-and-vectors.md`, Engine).
- **Benchmarks** (#157) start from the grouped cost figures predict reports (`estimated_bytes` per bucket count) and `TestMatrixGrouped_WorkerInvariant`.


## Handed on from U25

| Item | Source |
|---|---|
| Compose co-moment fits | [U25](U25-multivariate-tests-segmentation.md) #402 |
| Named populations | [U25](U25-multivariate-tests-segmentation.md) #403 |
| More GPA rotations | [U25](U25-multivariate-tests-segmentation.md) #406 |
| ~~Singularity rule~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #408 |
| ~~`MAT_DISTANCE` over date and extension groupers~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #411 |
| ~~Factor-score refusal drops the not-converged note~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #415 |

## Handed on from U27

| Item | Source |
|---|---|
| Sort / overlays / ratios over expanded vector-aggregate columns (refused as presentation only today) | [U27](U27-vector-metrics-aggregates.md) #420 |
| Group-centroid references for `ATTR_PROFILE_SIMILARITY` / `ATTR_VEC_DISTANCE` (an `AGG_VEC_MEAN` of a named filter, two-pass) | [U27](U27-vector-metrics-aggregates.md) #423 |
| Point-lookup-record references ("rows like customer #4411") | [U27](U27-vector-metrics-aggregates.md) #424 |
| `AGG_VEC_SD` (per-element standard deviation) | [U27](U27-vector-metrics-aggregates.md) #425 |
| Expose the registered-but-unexposed `chi_square` metric (takes column masses explicitly) through the crosstab-profile overlays (handed on again from [U28](U28-matrix-overlays.md)) | [U27](U27-vector-metrics-aggregates.md) #426 |

## Human inputs & decisions

- None.
