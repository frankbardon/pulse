---
id: U25
slug: multivariate-tests-segmentation
title: "Analysts can test whole profiles, flag unusual rows, score components and segment records"
track: Vector & matrix
size: L
status: done
depends_on: [U24]
soft_depends_on: []
blocks: [U30]
todo_items: [131, 132, 133, 134, 135, 136, 262, 263, 264, 295]
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

- [x] **#131** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `TEST_HOTELLING_T2`
- [x] **#132** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `TEST_MANOVA` (Wilks, Pillai, Hotelling–Lawley, Roy)
- [x] **#133** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `TEST_BARTLETT_SPHERICITY`
- [x] **#134** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `ATTR_MAHALANOBIS`
- [x] **#135** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `ATTR_PC_SCORE` (two-pass, `fit_on`)
- [x] **#136** (10. Vector & matrix — operators › E4 — Multivariate tests, fitted attributes, segmentation) `GROUP_KMEANS` (seeded k-means++, Euclidean-only, size-ordered labels)
- [x] **#262** (10. Vector & matrix — operators › Follow-ups from U24) `MAT_FACTOR`: multi-factor minres factor analysis, reusing `minresOneFactor`, the `PULSE_MATRIX_HEYWOOD` / `_NOT_IDENTIFIED` / `_NOT_CONVERGED` warnings (add it as an owner) and the PSD guard
- [x] **#263** (10. Vector & matrix — operators › Follow-ups from U24) Factor rotation (varimax, promax) for `MAT_FACTOR`, including a varimax option on `MAT_PCA` loadings (U24 ships unrotated components)
- [x] **#264** (10. Vector & matrix — operators › Follow-ups from U24) Link the `factor` glossary entry to `MAT_FACTOR` and add it as the third `measure_construct` declarer, clearing both exemption-ledger entries (`factor`, `measure_construct`) in `guidance_exemptions_test.go`
- [x] **#295** (Follow-ups from U40) Clustered and HAC covariance options on `REG_OLS` `Vcov`, so standard errors can allow for correlation across groups and over time (U24 ships the plain `vcov`); the worked example's standard errors were classical (independent rows) without it

## Scope

**In scope** (delivered; the unit grew from its original six operators)
- Multivariate tests: `TEST_HOTELLING_T2` (one-sample, two-sample pooled or unequal, paired), `TEST_MANOVA`, `TEST_BARTLETT_SPHERICITY`, `TEST_BOX_M`, `TEST_MARDIA`
- Fitted attributes: `ATTR_MAHALANOBIS`, `ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE`, on a shared two-pass fit/score base with `fit_on`
- k-means grouper `GROUP_KMEANS`
- Centroid distance matrix between a grouper's buckets (`MAT_DISTANCE`; local metric list)
- Factor analysis: `MAT_FACTOR` (minres, principal axis, ML; varimax, promax, oblimin) and a varimax option on `MAT_PCA` (#262–#264)
- Robust coefficient covariance on `REG_OLS`, `REG_GLM` and `REG_BAYES_LINEAR`: `RegressionSpec.Covariance` `hc0`–`hc3`, `cluster` (CR1), `hac` (#295)

**Out of scope**
- Row-to-row (n × n) distance matrices and the shared metric registry (U27)
- Panel HAC and two-way clustering, named populations, compose co-moment fits, per-`Groups` tests, the RMSEA interval and more rotation criteria (handed on below)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(multivariate-tests-segmentation/E<n>-S<m>): …`; close each epic with `milestone(multivariate-tests-segmentation/E<n>): vertical slice complete — <epic title>`.

- E1 — Profiles can be compared (Hotelling, MANOVA, Bartlett sphericity, Box's M, Mardia)
- E2 — Constructs can be extracted (rotation engine, `MAT_PCA` varimax, `MAT_FACTOR`)
- E3 — Rows can be scored (two-pass base, `ATTR_MAHALANOBIS`, `ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE`)
- E4 — Records can be segmented and compared (`GROUP_KMEANS`, `MAT_DISTANCE`)
- E5 — Standard errors are honest (`RegressionSpec.Covariance` on OLS, GLM and Bayes; the MMM worked example; this record)

Review record: [`reviews/U25-multivariate-review.md`](../reviews/U25-multivariate-review.md).

## Acceptance criteria

- [x] Hotelling/MANOVA match reference outputs on fixtures
- [x] Mahalanobis D² matches a reference computation; streaming correctly forces two-pass/buffered
- [x] k-means labels are identical across runs, worker counts and shard orders
- [x] All gates for new operators pass
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Coverage, example-tag and guidance gates
- `TestStreamability_*` updated for forced-buffered operators

## Update Demand companions

- `skills/op-test-*.md`, `op-attr-*.md`, `op-group-kmeans.md`
- `streaming-and-watching.md` forced-buffered list

## Handed on

Filed as TODO items (ids are TODO ids, not GitHub issues).

| Item | Owner |
|---|---|
| Panel HAC and two-way clustering | [U36](U36-reference-oracles.md) #401 |
| Compose co-moment fits | [U30](U30-matrix-extensions-hardening.md) #402 |
| Named populations | [U30](U30-matrix-extensions-hardening.md) #403 |
| Per-`Groups` statistical tests | [U35](U35-predict-runtime-parity.md) #404 |
| ~~RMSEA confidence interval~~ — resolved in U25 | [U36](U36-reference-oracles.md) #405 |
| More GPA rotations | [U30](U30-matrix-extensions-hardening.md) #406 |
| `MAT_DISTANCE` metrics into the shared registry | [U27](U27-vector-metrics-aggregates.md) #407 |
| ~~One singularity rule across tests and `MAT_PCA`~~ — resolved in U25 | [U30](U30-matrix-extensions-hardening.md) #408 |
| ~~Extension-test vector validation~~ — resolved in U25 | [U34](U34-extension-validation.md) #409 |
| ~~Weighted Bartlett factor-score oracle~~ — resolved in U25 | [U36](U36-reference-oracles.md) #410 |
| ~~`MAT_DISTANCE` over date and extension groupers~~ — resolved in U25 | [U30](U30-matrix-extensions-hardening.md) #411 |
| `statistical-testing.md` hard budget | [U32](U32-docs-audit.md) #412 |
| `op-test-*` soft budgets | [U32](U32-docs-audit.md) #413 |
| ~~Oracle regeneration notes~~ — resolved in U25 | [U36](U36-reference-oracles.md) #414 |
| ~~Factor-score refusal drops the not-converged note~~ — resolved in U25 | [U30](U30-matrix-extensions-hardening.md) #415 |

## Human inputs & decisions

- Statistics sign-off: done without a human reviewer; the automated review is [`reviews/U25-multivariate-review.md`](../reviews/U25-multivariate-review.md) and the human glance moved to U33 (#206)
