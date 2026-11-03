---
id: U78
slug: test-hygiene
title: "The test suite measures what it claims, hides no drift and carries no stale references."
track: API & release
size: M
status: not-started
depends_on: []
soft_depends_on: []
blocks: [U32, U66, U67, U68, U69, U70, U71, U72, U73, U74, U75, U76]
todo_items: [582, 583, 584, 585, 586, 587, 555, 598, 599, 600, 672, 673, 674, 675]
branch: test-hygiene
---

# U78 — test-hygiene

**Outcome:** The test suite measures what it claims, hides no drift and carries no stale references.

**Track:** API & release · **Size:** M · **Depends on:** none (runs first, in parallel with the other first-wave unit)

## Summary

Test-suite hygiene: invariance family migration, real coverage measurement, build-cache strategy, tier placement, pinned refusals, race-safe allocation ceilings, flaky memory tests, and removal of stale references, stale docs and drift-hiding skips.

Epics and stories are written by this unit's own flow-interview; this doc is the reduced inventory form.

## Owned TODO items

- [ ] **#582** The worker-invariance harness skips 24 plain knob byte-equality tests (parallel-decode core, shard core, weight, crosstab parallel decode, k-means, `u64` params); they are candidates for a core / weight invariance family in `internal/testutil/invariance` (reviewed, not migrated, in `baseline/invariance-inventory.md`). Deferred from ci-speed · [U78](units/U78-test-hygiene.md)
- [x] **#583** `TestPerPackageCoverageFloors` never measures coverage, so floors already unmet in both tiers go unnoticed (`encoding` 59.9 % vs 95, `internal/encoding` 85.7 vs 95, `internal/descriptor` 83.7 vs 95, `internal/skills` 90.7 vs 95, `internal/processing` 84.1 vs 85, `internal/service` 79.2 vs 80, `internal/statdist` 78.3 vs 90, and the csv / tsv / ndjson / parquet / excel adapters below 85). Make the gate measure and decide, per package, between raising coverage and restating the floor. Found in ci-speed. Done (test-hygiene E1-S1 + E1-S2): `make cover` measures both tiers with `-coverpkg=./...` against the one floor table `.github/coverage-floors.txt` (`citest cover-check`); `encoding`'s 59.9 % was a per-package measurement artifact (91.9 % with every test counted), and all eight short packages (`encoding`, `internal/encoding`, `internal/descriptor`, `internal/skills`, `internal/io/csv` / `tsv` / `excel`, `internal/mergegate`) were raised to their floors with tests — no floor restated · [U78](units/U78-test-hygiene.md)
- [ ] **#584** A per-job Go build-cache key, or a cache-warming workflow on `main`, so a cold build cache does not push a `test` / `test-heavy` job past the 8-minute wall (a cold full compile is ~52 s locally, ≈ 170 s on a CI runner). An owner call. Deferred from ci-speed · [U78](units/U78-test-hygiene.md)
- [ ] **#585** About 25 `internal/service` heavy tests run under 0.5 s and could return to the fast tier (they were moved wholesale with their file); check each against the 1 s budget at `-parallel 1`. Deferred from ci-speed · [U78](units/U78-test-hygiene.md)
- [ ] **#586** The hidden-operator refusal in `planRowTests` (`internal/processing/test.go`, the `p.exts.isHidden` resolver callback) is pinned by no test: reverting it fails nothing. Found in ci-speed (E2) · [U78](units/U78-test-hygiene.md)
- [ ] **#587** `testing.AllocsPerRun` ceilings fail under `-race` (4 on the pre-effort tree; the race runtime allocates): guard them with a `raceEnabled` constant as the root package does (`observability_race_off_test.go`). Found in ci-speed (E2) · [U78](units/U78-test-hygiene.md)
- [ ] **#555** Flaky CI memory-peak tests: `TestShardStream_CreateMemoryBoundedBySource/remapped` (failed once in U60 CI, passed on rerun) and `TestCrosstabMemory_CellChargeTable` (seen in U59). Harden them or make the budgets deterministic. Found in U60 · [U78](units/U78-test-hygiene.md)
- [x] **#598** Rename the two repo-wide numeric gates (`internal/testutil/fmagate`'s FMA-report and banned-math scans) into a prefix-matched non-skippable family (and list them in CLAUDE.md "Non-Skippable CI Gates") once CLAUDE.md has headroom; they are binding and run in CI today. Deferred from U61 (E5-S2). Done (test-hygiene E5-S1): renamed `TestCrossArchFMAReport_RepoWide` (heavy) and `TestCrossArchBannedMath_RepoWide` (fast); `TestCrossArch` is the ninth prefix of `TestClaudeMdMentionsAllNonSkippableGates`, CLAUDE.md lists both under "Cross-arch determinism:" (room made by trimming four always-load bullets whose long form already sat in `.claude/reference/`), and the per-gate prose moved into the new slice `.claude/reference/update-demand/gates-cross-arch.md` · [U78](units/U78-test-hygiene.md)
- [ ] **#599** Shrink the `MAT_SEM` cross-architecture residual: route `internal/sem`'s gonum `mat` calls (`Mul`, `Inverse`, `LogDet`, Cholesky) through `linalg`'s FMA-free gonum-order SPD port, so fits that converge stop drifting (early-stop fits can still differ through the optimiser). Deferred from U61 (E4-S4) · [U78](units/U78-test-hygiene.md)
- [ ] **#600** Regression unit tests' generated inputs still differ across architectures (test-only generators, not engine paths): barrier them or draw them with `detmath.NormFloat64`, so those tests can assert bits on both. Found in U61 (E4-S2) · [U78](units/U78-test-hygiene.md)
- [ ] **#672** Reword the 12 code comments that cite a CLOSED item as `TODO #N` (#157, #199, #270, #315, #316, #317, #360, #408, #409, #410, #411, #488; e.g. `cmd/pulse/showcase_flags_test.go:12`, `linalg/comoment_layout_test.go:32`, `internal/exprenv/calendar.go:22`) to plain "see #N", and check the #410 hint at `internal/processing/attribute_factor_score_test.go:111` that a proper two-factor Heywood-free fixture was delivered (reopen if not); resolve the bare `TODO` at `internal/service/matrix_scale_bench_test.go:26` · [U78](units/U78-test-hygiene.md)
- [ ] **#673** Stale "Phase 0 stub" documentation in the regression package: `internal/processing/regression/regression.go:4-12,63` and `internal/processing/processor.go:2005` still say every operator returns `PROCESSING_REGRESSION_NOT_IMPLEMENTED` although OLS, penalized, GLM, Bayes and the modifiers have shipped; fix the comments and check whether the stub engine is still reachable · [U78](units/U78-test-hygiene.md)
- [ ] **#674** `internal/synth/residual_corr_internal_test.go:378` skips on "fixture drift: gamma acquired predictors", hiding the drift the test exists to catch: convert it to a failure · [U78](units/U78-test-hygiene.md)
- [ ] **#675** Audit the remaining ~50 unowned `t.Skip` / `t.Skipf` calls (none cites an item): convert vacuous-precondition skips (`internal/descriptor/ontology_instance_test.go:158,266`, `intents_test.go:180`, `skills/virtual_test.go:41`, `encoding/description_test.go:103`) to asserted preconditions, check the `errors/*_test.go` data-absent skips, and verify the replacement test named at `internal/service/weight_parity_heavy_test.go:89` exists · [U78](units/U78-test-hygiene.md)
