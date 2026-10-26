---
id: U61
slug: cross-arch-determinism
title: "Every numeric result path gives identical bits on arm64 and amd64, and a repo-wide gate keeps it that way"
track: API & release
size: L
status: not-started
depends_on: []
soft_depends_on: [U58, U60]
blocks: [U32]
todo_items: [554, 569]
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

- [ ] **#554** remaining core cross-architecture numeric drift: full audit, fixes, and a repo-wide gate banning stdlib transcendentals / contractible products on numeric result paths
- [ ] **#569** the shared MMM test fixture (`internal/mmmfixture`): barrier the flagged products and give the package an FMA gate

## Scope

**In scope**
- The audit of every `math.*` transcendental and contractible product on a result path
- Moving the confirmed sites onto `linalg/detmath` with barriered products
- A repo-wide gate (new `TestNo...` prefix-matched names go in CLAUDE.md "Non-Skippable CI Gates")
- A decision on regression standard errors computed through gonum

**Out of scope**
- Changing any figure beyond last-ulp rounding on the affected path
- Bit-pinning goldens that are not already architecture-pinned (a figure that moves by an ulp is documented in the migration table)

## Epics & stories

Not yet planned. Expect one epic per area (distributions and tests; processing / service / types call sites; regression SEs) and a closing gate story.

## Acceptance criteria

- [ ] Every site in the seed list is fixed or recorded as an accepted residual with a reason
- [ ] A repo-wide gate fails when a stdlib transcendental or contractible product appears on a numeric result path
- [ ] An arm64 run and an amd64 run agree bit-for-bit on the affected figures
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

To be defined by the audit; the gate follows the `NoFMAContraction` pattern.

## Update Demand companions

- `docs/src/library/linalg.md` (Deterministic math), the migration table for any figure that moves, CLAUDE.md "Non-Skippable CI Gates" for the new gate, `matrix-and-vectors.md` if the `linalg` policy changes

## Handed on to later work

None yet.
