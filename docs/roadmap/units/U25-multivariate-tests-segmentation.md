---
id: U25
slug: multivariate-tests-segmentation
title: "Analysts can test whole profiles, flag unusual rows, score components and segment records"
track: Vector & matrix
size: L
status: not-started
depends_on: [U24]
soft_depends_on: []
blocks: [U30]
todo_items: [131, 132, 133, 134, 135, 136, 262, 263, 264]
branch: multivariate-tests-segmentation
---

# U25 — multivariate-tests-segmentation

**Outcome:** Analysts can test whole profiles, flag unusual rows, score components and segment records.

**Track:** Vector & matrix · **Size:** L · **Depends on:** [U24](U24-matrix-operators.md) · **Unblocks:** [U30](U30-matrix-extensions-hardening.md)

## Summary

`TEST_HOTELLING_T2`, `TEST_MANOVA`, `TEST_BARTLETT_SPHERICITY`, `ATTR_MAHALANOBIS`, `ATTR_PC_SCORE` (two-pass fit/score with `fit_on`), and `GROUP_KMEANS` (seeded k-means++, Euclidean-only, size-ordered labels).

## References

**Theme documents (read before starting):**
- [vector-matrix 03 — Multivariate statistics](../v1.0.0-vector-matrix/03-multivariate-statistics.md) — Multivariate tests, Row-level attributes, Segmentation grouper
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E4

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#131** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `TEST_HOTELLING_T2`
- [ ] **#132** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `TEST_MANOVA` (Wilks, Pillai, Hotelling–Lawley, Roy)
- [ ] **#133** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `TEST_BARTLETT_SPHERICITY`
- [ ] **#134** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `ATTR_MAHALANOBIS`
- [ ] **#135** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `ATTR_PC_SCORE` (two-pass, `fit_on`)
- [ ] **#136** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `GROUP_KMEANS` (seeded k-means++, Euclidean-only, size-ordered labels)
- [ ] **#262** (10. Vector & matrix — operators › Follow-ups from U24) `MAT_FACTOR`: multi-factor minres factor analysis, reusing `minresOneFactor`, the `PULSE_MATRIX_HEYWOOD` / `_NOT_IDENTIFIED` / `_NOT_CONVERGED` warnings (add it as an owner) and the PSD guard
- [ ] **#263** (10. Vector & matrix — operators › Follow-ups from U24) Factor rotation (varimax, promax) for `MAT_FACTOR`, including a varimax option on `MAT_PCA` loadings (U24 ships unrotated components)
- [ ] **#264** (10. Vector & matrix — operators › Follow-ups from U24) Link the `factor` glossary entry to `MAT_FACTOR` and add it as the third `measure_construct` declarer, clearing both exemption-ledger entries (`factor`, `measure_construct`) in `guidance_exemptions_test.go`

## Scope

**In scope**
- Three tests
- Two fitted attributes
- k-means grouper

**Out of scope**
- Factor analysis, distance matrices (stretch)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(multivariate-tests-segmentation/E<n>-S<m>): …`; close each epic with `milestone(multivariate-tests-segmentation/E<n>): vertical slice complete — <epic title>`.

### E1 — Profiles can be compared
- S1: `TEST_HOTELLING_T2`
- S2: `TEST_MANOVA` (four statistics; Pillai headline)
- S3: `TEST_BARTLETT_SPHERICITY`

### E2 — Rows can be scored
- S1: two-pass fit/score infrastructure with `fit_on`
- S2: `ATTR_MAHALANOBIS`
- S3: `ATTR_PC_SCORE`

### E3 — Records can be segmented
- S1: `GROUP_KMEANS` with deterministic labels; buffered; memory in predict

## Acceptance criteria

- [ ] Hotelling/MANOVA match reference outputs on fixtures
- [ ] Mahalanobis D² matches a reference computation; streaming correctly forces two-pass/buffered
- [ ] k-means labels are identical across runs, worker counts and shard orders
- [ ] All gates for new operators pass
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Coverage, example-tag and guidance gates
- `TestStreamability_*` updated for forced-buffered operators

## Update Demand companions

- `skills/op-test-*.md`, `op-attr-*.md`, `op-group-kmeans.md`
- `streaming-and-watching.md` forced-buffered list

## Human inputs & decisions

- Statistics reviewer
