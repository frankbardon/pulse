---
id: U61
slug: cross-arch-determinism
title: "Every numeric result path gives identical bits on arm64 and amd64, and a repo-wide gate keeps it that way"
track: API & release
size: L
status: done
depends_on: []
soft_depends_on: [U58, U60]
blocks: [U32]
todo_items: [554, 569, 601]
branch: cross-arch-determinism
---

# U61 — cross-arch-determinism

**Outcome:** Every numeric result path gives identical bits on arm64 and amd64, and a repo-wide gate keeps it that way.

**Track:** API & release · **Size:** L · **Depends on:** — · **Soft:** [U58](U58-expr-math-lags-params.md), [U60](U60-mmm-estimation.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

Stub, created at the close of [U60](U60-mmm-estimation.md) on the owner's decision (2026-10-25). [U58](U58-expr-math-lags-params.md) made the expression maths and synth generation architecture-identical, and U60 moved `WIN_DISTRIBUTED_LAG` weights and the Student-t quantile chain onto the public `linalg/detmath` (barriered products, no stdlib transcendentals). The rest of the core still calls stdlib `math.*` transcendentals or leaves contractible multiply-adds on result paths, so an arm64 build (which fuses `a*b+c`) and an amd64 build can differ in the last bits of a figure.

This unit audits every numeric result path, fixes the drift, and adds a repo-wide gate that bans stdlib transcendentals and contractible products on those paths. Pattern to follow: `linalg/detmath` plus the `NoFMAContraction` gates (`TestOptimize_NoFMAContraction`, the pack's arm64 FMA gate).

## Seed list (from U60 E4-S4; to be confirmed by the audit)

- `internal/statdist/normal.go:26,27,61,70` — `NormalQuantile` (`math.Erfc` / `Exp` / `Log` plus arm64 contraction): the Wald and normal CI bounds and the normal credible interval
- `internal/processing/test_stat.go:180` — chi-square incomplete-gamma prefactor (`math.Exp` / `Sqrt`)
- `linalg/metric/eval.go:280,292` — the Aitchison metric's `math.Log` (a documented residual)
- `TEST_T` / paired-t CI inputs: `internal/processing/test_t.go:272,273,357,358`, `test_paired.go:107,108`, `welford_bucket.go:24`, `internal/weighting/inference.go:113,117`
- Regression standard errors via gonum: there is no bit contract today, so this needs its own design call (port the decompositions, or document a tolerance contract)
- `internal/mmmfixture/joint.go:198` (price generator), `~296-307` (`TrueOptimum`) and `joint_test.go:69`: products the FMA report flags in the shared MMM fixture, which has no FMA gate of its own (#569, found in U62)
- About 20 unaudited `math.Exp` / `Log` / `Pow` / `Lgamma` / `Erf` call sites in `internal/processing`, `internal/service` and `types`

## References

- `internal/detmath` became public as `linalg/detmath` in U60 (E4-S4); `docs/src/library/linalg.md` (Deterministic math)
- `.claude/reference/matrix-and-vectors.md` (`linalg` policy: two backends, FMA-free kernels)
- [Embedder migration: Changes from U60](../v1.0.0-api-and-release/03-embedder-migration.md) (the last-ulp movement already shipped for lag weights and t quantiles)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#554** remaining core cross-architecture numeric drift: full audit, fixes, and a repo-wide gate banning stdlib transcendentals / contractible products on numeric result paths
- [x] **#569** the shared MMM test fixture (`internal/mmmfixture`): barrier the flagged products and give the package an FMA gate
- [x] **#601** `TestMatrixCorrelation_PearsonParity`: drop the amd64-only guard on its bitwise leg (found in E5-S3, fixed in-unit)

## Scope

**In scope**
- The audit of every `math.*` transcendental and contractible product on a result path
- Moving the confirmed sites onto `linalg/detmath` with barriered products
- A repo-wide gate (new `TestNo...` prefix-matched names go in CLAUDE.md "Non-Skippable CI Gates")
- A decision on regression standard errors computed through gonum

**Out of scope**
- Changing any figure beyond last-ulp rounding on the affected path
- Bit-pinning goldens that are not already architecture-pinned (a figure that moves by an ulp is documented in the migration table)

## Decisions (owner, 2026-10-31)

- **Regression SPD path:** an FMA-free pure-Go port of gonum v0.17.0's amd64 operation order (like `internal/sem/dot.go`), not the reference Cholesky: amd64 moves 0 ulp, arm64 converges.
- **Split tolerance:** PCA, `MAT_FACTOR` and `TEST_MANOVA` are pinned at 1e-12 relative. `MAT_SEM` pins only fully converged fits; early-stop fits and the converged-flag flip are a documented residual.
- **`AutoHACLags`:** fixed in the unit (exact integer lag), with a migration row.
- **Verification:** a committed fingerprint golden in the normal test tier, checked by CI's linux/amd64 run. Nothing committed depends on Docker.
- Orchestrator calls listed for review in the PR body: rounding-class arm64 moves just over the 1e-14 line (Shapiro–Wilk z 2.2e-14), `TestMetricImportBoundary` widened to `linalg/detmath`, `types` kept off `detmath`, the GLM links' `ExpAMD64FMA`, and the gate names kept outside a prefix family (CLAUDE.md headroom).

## Epics & stories

### E1 — Foundation (done)
- S1 `detmath` `Erfc` / `Atanh` / `Tanh`, S2 `statdist` distuv replacements, S3 repo-wide FMA and banned-math reports with an allowlist, S4 the fingerprint golden
### E2 — Distributions and tests (done)
- S1 normal quantile and chi-square prefactor, S2 t / paired-t / Welford / weighted-inference CI inputs, S3 parametric `TEST_*`, S4 nonparametric `TEST_*`
### E3 — Processing, service and types call sites (done)
- S1 aggregator, window, feature and overlay sites, S2 matrix factor ML, rotation, Aitchison and the ridge schedule, S3 service, types, root, SPSS and regadapt sites
### E4 — Regression, SEM and decompositions (done)
- S1 the gonum-order SPD port, S2 regression transcendentals and products, S3 SEM arithmetic, S4 the eigen / SVD / QR residual at 1e-12, S5 the exact `AutoHACLags`
### E5 — Closing gates (done)
- S1 `mmmfixture` barriered and gated, S2 the repo-wide gates binding, S3 coverage list, migration rows and roadmap ticks

## Acceptance criteria

- [x] Every site in the seed list is fixed or recorded as an accepted residual with a reason (`docs/src/library/linalg.md`, What is covered across architectures)
- [x] A repo-wide gate fails when a stdlib transcendental or contractible product appears on a numeric result path (`TestBannedMathReport_RepoWide`, `TestFMAReport_RepoWide`)
- [x] An arm64 run and an amd64 run agree bit-for-bit on the affected figures (`internal/fingerprint`, checked by CI's linux/amd64 run)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit)): the full gate set and CI run at the PR

## Gates & tests

- Repo-wide: `TestFMAReport_RepoWide` (heavy) and `TestBannedMathReport_RepoWide` (fast) over `internal/testutil/fmagate/testdata/numeric-allowlist.txt`
- Bits: `TestFingerprint_Golden` (`internal/fingerprint`), `TestSPD_MatchesGonumOrder` (binds on amd64), `TestSPD_BitPinned`, `TestStatdist_ExpAMD64FMA`, `TestSEM_ArithmeticBitsPinned`, `TestTestStat_BitsPinned`, the `detmath` golden-bit suites
- Tolerance: `TestDecomposition_Tolerance` (1e-12 relative)
- Per-package FMA gates: `TestStatdist_NoFMAContraction`, `TestMMMFixture_NoFMAContraction` and the existing `detmath` / `optimize` / `mcmc` / `sem` / `distlagparams` / `regmmm` gates

## Update Demand companions

- `docs/src/library/linalg.md` (Deterministic math; What is covered across architectures), the [migration table](../v1.0.0-api-and-release/03-embedder-migration.md) (Changes from U61), `.claude/reference/matrix-and-vectors/linalg.md` (Backend policy, the repo-wide gates), `.claude/reference/update-demand/gates.md`, `.claude/reference/agent-practices.md` (Cross-architecture numerics)

## Handed on to later work

Filed as TODO items in [`TODO.md`](../TODO.md) (ids are TODO ids, not GitHub issues):

| Item | Owner |
|---|---|
| #598 Rename the two repo-wide gates into a prefix-matched non-skippable family once CLAUDE.md has headroom | [U33](U33-v1-release.md) |
| #599 Route `internal/sem`'s gonum `mat` calls through the FMA-free SPD port to shrink the `MAT_SEM` residual | [U59](U59-sem-extensions.md) |
| #600 Regression unit tests' generated inputs still differ across architectures (test-only) | [U49](U49-model-workbench.md) |
