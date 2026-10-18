---
id: U30
slug: matrix-extensions-hardening
title: "Embedders can add their own matrix operators, and the matrix stack is proven at scale"
track: Vector & matrix
size: XL
status: done
depends_on: [U25, U28, U29]
soft_depends_on: []
blocks: [U32]
todo_items: [156, 157, 158, 270, 402, 403, 406, 408, 411, 415, 420, 423, 424, 425, 426, 449]
branch: matrix-extensions-hardening
---

# U30 — matrix-extensions-hardening

**Outcome:** Embedders can add their own matrix operators, and the matrix stack is proven at scale.

**Track:** Vector & matrix · **Size:** XL · **Depends on:** [U25](U25-multivariate-tests-segmentation.md), [U28](U28-matrix-overlays.md), [U29](U29-vector-field-types.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

`MatrixOpRegistration` and the `MAT` naming-policy namespace with probe validation; benchmarks at p = 50/256 over 1M/10M rows (peak heap, shard scaling); examples-library entries for every new operator.

As delivered (the effort grew to nine epics): embedder `MAT_*` operators with built-in parity; vector fits shared through Compose `fits`, saved as population files and loaded or registered by name; centroid, population and stored-record comparison references; expanded vector element columns as ordinary columns everywhere (sort, windows, post-tests, overlays, chains, crosstab `element` cells); `AGG_VEC_SD`, per-pair n_eff under pairwise probability weights, chi-square profile distances on `OVERLAY_CORRESPONDENCE`; the generic GPA rotation driver with `quartimax`, `entropy`, `geomin` and `geomin_orthogonal`; the `flows` overlays `OVERLAY_SYMMETRY` and `OVERLAY_RETENTION`; overlay Ref / Scope / host refusals shared by predict and runtime; undefined figures null on every arm; matrix scale benchmarks and shard-archive memory accounting; examples for all of it; and the agent context diet (reference slices, `sites`, personas).

## References

**Theme documents (read before starting):**
- [vector-matrix 05 — Cross-cutting concerns](../v1.0.0-vector-matrix/05-cross-cutting.md) — X7 Extension points
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E10 (committed part)

**Records written by this unit:**
- [U30 flow overlays review](../reviews/U30-flows-review.md) — statistical review of `OVERLAY_SYMMETRY` / `OVERLAY_RETENTION`; human sign-off at [U33](U33-v1-release.md) (#206)
- [`docs/internals/BENCHMARKS_BASELINE.md`](../../internals/BENCHMARKS_BASELINE.md) — matrix scale and co-moment baselines
- [03-embedder-migration.md](../v1.0.0-api-and-release/03-embedder-migration.md) — "Changes from U30" (alpha.13 behaviour breaks)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#156** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) `MatrixOpRegistration` and the `MAT` naming-policy namespace; probe validation; done in E1-S1..S4
- [x] **#157** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) Benchmarks: p = 50 / 256 at 1M and 10M rows; peak heap; shard scaling; done in E8-S1 (the 1M x p = 50 tier has committed baselines; 1M x p = 256 and 10M are opt-in via `make bench-scale`), with shard-archive memory accounting in E8-S3..S5
- [x] **#158** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) Examples-library entries for every new operator; done in E8-S2
- [x] **#270** (10. Vector & matrix — operators › Follow-ups from U24) Matrix hardening: a per-pair Σw² for probability-weighted pairwise `item_sd` in the co-moment state; done in E5-S2
- [x] **#402** (10. Vector & matrix — operators › Follow-ups from U25) Compose co-moment fits: fit a vector battery's co-moment state once in a compose `fits` entry and let several slots (`MAT_*`, `ATTR_MAHALANOBIS`, `ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE`) reuse it, as regressions already do; done in E2-S1 / E2-S2
- [x] **#403** (10. Vector & matrix — operators › Follow-ups from U25) Named populations: save a fitted vector population (mean, covariance, loadings) under a name and score other cohorts against it, instead of re-fitting with `fit_on` per request; done in E2-S3 / E2-S4
- [x] **#406** (10. Vector & matrix — operators › Follow-ups from U25) More GPA rotations for `MAT_FACTOR` / `MAT_PCA`: quartimax, geomin, oblimax, and the `GPArotation` criteria beyond varimax, promax and oblimin, on the shared rotation engine; done in U30 E6-S2: `quartimax`, `entropy`, `geomin_orthogonal` (orthogonal; `MAT_PCA` / `ATTR_PC_SCORE` too) and `geomin` (oblique; `params.epsilon`), refused oblique on PCA with `rotation_oblique_unsupported`; `oblimax` not offered (degenerate under GPFoblq on most inputs, and quartimax when orthogonal) — rationale in `.claude/reference/matrix-and-vectors/matrix-decomposition.md` (Rotation engine)
- [x] **#408** (10. Vector & matrix — operators › Follow-ups from U25) One singularity rule across the multivariate stack: the tests decide singular by `gonum` `SymEigen` against `RankTolerance`, while `MAT_PCA` is singular only when `InverseSPD` fails; align them and add the borderline case to the oracle
- [x] **#411** (10. Vector & matrix — operators › Follow-ups from U25) `MAT_DISTANCE` `between` refuses date-family groupers (the time-zone pass does not reach `params`) and extension groupers; resolve the zone for the date family and give extension groupers a centroid hook, or document the refusal as final
- [x] **#415** (10. Vector & matrix — operators › Follow-ups from U25) `ATTR_FACTOR_SCORE`: a refusal (`PULSE_MATRIX_NOT_IDENTIFIED` / `_HEYWOOD`) drops the fit's not-converged note; carry it on the refusal warning
- [x] **#420** (10. Vector & matrix — operators › Follow-ups from U27) Sort, overlays and ratios over a vector aggregation's expanded element columns (`params.expand`, `<label>[<element label>]`); done in E4-S1..S4
- [x] **#423** (10. Vector & matrix — operators › Follow-ups from U27) Group-centroid references for `ATTR_PROFILE_SIMILARITY` / `ATTR_VEC_DISTANCE`; done in E3-S1
- [x] **#424** (10. Vector & matrix — operators › Follow-ups from U27) Point-lookup-record references; done in E3-S2
- [x] **#425** (10. Vector & matrix — operators › Follow-ups from U27) `AGG_VEC_SD`, the per-element standard deviation of a vector; done in E5-S1
- [x] **#426** (10. Vector & matrix — operators › Follow-ups from U27) Expose the registered-but-unexposed `chi_square` metric through a consumer; done in E5-S3 (`OVERLAY_CORRESPONDENCE` row / column distances)
- [x] **#449** (10. Vector & matrix — operators › Follow-ups from U28) The `flows` intent needs ≥3 declaring operators; `OVERLAY_MARKOV` is its only declarer. Add genuine flows operators and drop the `intentDeclarerExemptions` entry; done in E7-S1 / E7-S2 (the previous-state attribute is #452, U50)

## Scope

**In scope**
- Extension registration + probe
- Benchmarks
- Examples completeness

**Out of scope**
- Stretch operators

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(matrix-extensions-hardening/E<n>-S<m>): …`; close each epic with `milestone(matrix-extensions-hardening/E<n>): vertical slice complete — <epic title>`.

As delivered (the plan's two epics grew to nine; E9 ran first):

### E1 — Embedders register their own matrix operators
- S1: `extend` matrix contract, `MatrixOpRegistration`, `MAT` naming policy · S2: run-probe at `pulse.New` · S3: end-to-end execution with built-in parity (engine-owned `missing` / `max_drop_share`, opt-in multiplicity via `PValues`) · S4: bitwise worker-invariance gate + docs

### E2 — Fit a vector battery once, save it, score other cohorts
- S1: fitted attributes score a prebuilt vector state (`params.fit`) · S2: compose `fits` accept a vector fit; `MAT_*` accept `fit` · S3: save populations; `populations` request slot · S4: registered populations; coded directory-loader collisions · S5 (owner-added): fit-bound fitted attributes are row-local; predict aligns in-batch fit labels · G1: fitted-state goldens tolerate cross-arch ulp drift

### E3 — Compare rows to centroids, populations and stored records
- S1: centroid and population references · S2: stored-record references through the point-lookup index

### E4 — Expanded vector elements are ordinary columns
- S1: sort, windows, post-tests · S2: overlay hosts · S3: chain stages (`f64`) · S4: crosstab `element` cells and quoted return paths · S5 / S6 (owner-added): undefined figures are null, rounds 1 and 2

### E5 — Vector and matrix statistics are complete and exact
- S1: `AGG_VEC_SD` · S2: per-pair n_eff in reliability / PCA covariance (#270) · S3: chi-square profile distances on `OVERLAY_CORRESPONDENCE` (#426) · G1: portable inputs for the frozen `AGG_VEC_MEAN` / `SUM` words

### E6 — More factor rotations
- S1: generic GPForth + GPFoblq driver · S2: `quartimax`, `entropy`, `geomin`, `geomin_orthogonal` (+ `epsilon`) · G1: Bartlett sign check never reads a NaN sign bit

### E7 — Analysts read flows between states
- S1: `OVERLAY_SYMMETRY` (McNemar / Bowker) · S2: `OVERLAY_RETENTION` · S3 / S4 / S5 (owner-added): crosstab, non-crosstab and scalar-host overlay refusals shared by predict and runtime; chain stages refuse a crosstab

### E8 — Proven at scale, documented by example, roadmap closed
- S1: matrix scale benchmarks; stale co-moment byte estimate fixed · S2: examples, the last read-past schema decodes made layout-only, roadmap close-out · S3 / S4 / S5 (owner-added): predict charges shard-archive memory; archive record counts and the scan skip dictionaries; allocation-free listwise `CoMoment.Add`

### E9 — Agent context diet
- S1 / S4: oversized reference docs split into index + slices · S2: the `sites` change-site generator · S3: agent working practices in the personas · G1: gofmt check through the selected toolchain

## Acceptance criteria

- [x] An extension `MAT_ACME_X_Y` registers, appears in manifest/predict/MCP identically to built-ins, and is probe-validated
- [x] Benchmarks are committed with peak-heap figures
- [x] `TestEveryOperatorHasAnExampleTag` passes for all new operators
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestExtensions_*` extended
- `TestEveryOperatorHasAnExampleTag`

## Update Demand companions

- `docs/src/internals/extension-points.md`; CLAUDE.md naming-policy regex

## Handed on from U16

- ~~**`BlockMerger` through `extend`.**~~ Resolved in U30 E1: an extension `MAT_*` folds through the engine's blocked merge, `MAT_` joined the naming policy, and `TestExtensionMatrix_WorkerInvariant` pins it bitwise across worker counts. The original note: the blocked merge opt-in (`internal/processing/block_merge.go`) is internal; `MatrixOpRegistration` (#156) must expose it, or an embedder `MAT_*`-style operator is bit-unstable across worker counts. Today `MAT_` is NOT in the extension naming regex and `matrixFinalizers` has no extension registry (`.claude/reference/matrix-and-vectors.md`, Engine).
- ~~**Benchmarks**~~ Resolved in U30 E8-S1: `BenchmarkMatrixScaleGrouped` and the worker sweep. The original note: benchmarks (#157) start from the grouped cost figures predict reports (`estimated_bytes` per bucket count) and `TestMatrixGrouped_WorkerInvariant`.


## Handed on from U25

| Item | Source |
|---|---|
| ~~Compose co-moment fits~~ — resolved in U30 (E2) | [U25](U25-multivariate-tests-segmentation.md) #402 |
| ~~Named populations~~ — resolved in U30 (E2) | [U25](U25-multivariate-tests-segmentation.md) #403 |
| ~~More GPA rotations~~ — resolved in U30 (E6) | [U25](U25-multivariate-tests-segmentation.md) #406 |
| ~~Singularity rule~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #408 |
| ~~`MAT_DISTANCE` over date and extension groupers~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #411 |
| ~~Factor-score refusal drops the not-converged note~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #415 |

## Handed on from U27

| Item | Source |
|---|---|
| ~~Sort / overlays / ratios over expanded vector-aggregate columns~~ — resolved in U30 (E4) | [U27](U27-vector-metrics-aggregates.md) #420 |
| ~~Group-centroid references for `ATTR_PROFILE_SIMILARITY` / `ATTR_VEC_DISTANCE`~~ — resolved in U30 (E3) | [U27](U27-vector-metrics-aggregates.md) #423 |
| ~~Point-lookup-record references~~ — resolved in U30 (E3) | [U27](U27-vector-metrics-aggregates.md) #424 |
| ~~`AGG_VEC_SD`~~ — resolved in U30 (E5) | [U27](U27-vector-metrics-aggregates.md) #425 |
| ~~Expose the `chi_square` metric through the crosstab-profile overlays~~ — resolved in U30 (E5; handed on again from [U28](U28-matrix-overlays.md)) | [U27](U27-vector-metrics-aggregates.md) #426 |

## Handed on

| Item | Destination |
|---|---|
| `ATTR_PREVIOUS_STATE`, a per-subject previous-state attribute (the fourth `flows` declarer) | [U55](U55-previous-state.md) #452 (from #449, E7-S2; first filed under [U50](U50-local-time.md)) |
| `sites` change-site tool gaps (non-enum param forms, metric-registry consumers, false category-norm warnings, subset hand lists, shared-oracle attribution) | [U32](U32-docs-audit.md) #453 |
| `OVERLAY_SYMMETRY` exact binomial McNemar (`params.exact`) and the discordant odds-ratio effect size | [U36](U36-reference-oracles.md) #454 (owner decision 2026-10-14) |
| Streaming shard-archive open for Process runs (section readers instead of reading the whole archive), then drop predict's archive byte term | [U51](U51-archive-scale.md) #455 (owner decision 2026-10-14) |
| Weighted pairwise collinearity oracle case (PCA and reliability landed in E5-S2) | [U36](U36-reference-oracles.md) #266 (note added) |
| A one-trait rating battery for the reliability / PCA examples: `survey.pulse` (two factors) now exists | [U31](U31-guidance-guides.md) #269 (note added) |

## Landed deviations

- **`oblimax` dropped** (owner decision 2026-10-14). #406 named it, but under GPFoblq it collapses factors (singular Φ, loadings around 10⁶) on 6 of the 8 oracle inputs, and its orthogonal form is quartimax. It is not offered, and no roadmap item tracks it. The rationale is in `.claude/reference/matrix-and-vectors/matrix-decomposition.md` (Rotation engine).
- GPForth varimax and the production SVD varimax agree within 2e-3, not the matrix tolerance: the two algorithms stop by different rules (R's own pair differs by up to 8.7e-4). Step-for-step correctness is pinned by an R GPForth oracle at 1e-9; production varimax is unchanged (E6-S1). Geomin runs that are input-sensitive in R itself compare within the GPA band 1e-4 (E6-S2).
- Populations reuse the `PULSE_MODEL_*` codes (no `PULSE_POPULATION_*`); one new code, `PULSE_MODEL_KIND_MISMATCH` (E2).
- Listwise `CoMoment.Add` became allocation-free through a reverse-order walk, not a scratch buffer: a buffer would be shared across `Clone` and change the pinned layout (E8-S5).
- Overlay refusal parity grew three owner-added stories (E7-S3..S5), which superseded U46's "SCALAR host not judged" and closed #439 and #442 (U35).
- Undefined figures (0 → null) were fixed across the aggregators and overlay hosts in E4-S5 / S6. These are behaviour breaks, listed in the migration guide's "Changes from U30".

## Human inputs & decisions

- Owner decisions during the unit (2026-10-13 / 14): engine-owned `missing` / `max_drop_share` stripped before an extension sees `MatrixInput.Params` (a registration declaring them is refused); extension multiplicity is opt-in via `PValues`; quoted return-path segments; undefined figures are null everywhere; empty pairs dropped by `OVERLAY_SYMMETRY`; exact McNemar and its effect size deferred (#454); shard-archive memory accounted now, streaming open deferred (#455); oblimax dropped.

