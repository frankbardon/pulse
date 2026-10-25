---
id: U60
slug: mmm-estimation
title: "A media-mix analyst jointly fits response curves, searches adstock decays with a seeded sweep and reallocates budget with a constrained optimizer"
track: Vector & matrix
size: L
status: in-progress
depends_on: []
soft_depends_on: [U40, U53, U57, U58, U59]
blocks: [U32]
todo_items: [489, 539, 540, 541, 542, 543, 544, 545, 546, 547]
branch: mmm-estimation
---

# U60 — mmm-estimation

**Outcome:** A media-mix analyst jointly fits response curves, searches adstock decays with a seeded sweep and reallocates budget with a constrained optimizer.

**Track:** Vector & matrix · **Size:** L · **Depends on:** — · **Soft:** [U40](U40-compose-sweep.md), [U53](U53-custom-regressions.md), [U57](U57-transformed-predictors.md), [U58](U58-expr-math-lags-params.md), [U59](U59-sem-extensions.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

[U53](U53-custom-regressions.md) shipped the media-mix primitives and [U57](U57-transformed-predictors.md) let a regression read a window of its own request, but three things were still done by hand: the Hill curve's half-saturation and shape were fitted by a test-only regression, the decay search was a grid (30^4 slots for four channels), and the budget split was a scenario written out slot by slot. This unit closes all three with general tools, and ships the one industry-specific piece as a pack.

- **Solver (E1).** A public `optimize` package: `Minimize` (SLSQP with equality / inequality constraints, bounds, multipliers) and `LeastSquares` (bounded Levenberg–Marquardt with exact-zero bounds and `JᵀJ`). Both are FMA-free, pinned by R oracles (`nloptr` SLSQP, `minpack.lm`), and `MAT_SEM` and the test-only Hill regression run on them.
- **Seeded sweep (E2).** Compose `sweep` modes `random` and `lhs` with `samples`, `rng_seed` and axis `range`; the echoed `sweep_seed` makes a run reproducible. Alongside, three contract clean-ups the sweep depended on: `WIN_ADSTOCK` became `WIN_DISTRIBUTED_LAG`, and an unknown window / regression / test type is one code on predict and every runtime surface.
- **Compose `optimize` (E3).** A block that maximises or minimises an expression over named variables, subject to bounds and constraints, reading fitted coefficients with `coef()`. It returns the optimum, each variable's marginal, each constraint's multiplier and active bounds, and fails alone.
- **MMM pack (E4).** `contrib/mmmpulse` registers `REG_MMM_HILL`, a joint nonlinear least-squares fit of every channel's effect and saturation curve. The pack's pass bar on a 156-week, four-channel joint-truth fixture is met on the root library, the CLI path and `mcpserve`. `linalg/detmath` was made public so the pack and core share one deterministic maths set.

Status: implemented on branch `mmm-estimation`; targets `v1.0.0-alpha.27` with a lockstep `contrib/mmmpulse` tag. Nothing is released or tagged by this unit's close-out.

## References

- `.claude/reference/update-demand/compose-optimize.md`, `execution-modes/compose.md`, `update-demand.md`, `architecture.md` (packages), `feature-profiles.md`, `guided-analysis.md`
- `internal/skills/compose-sweep-sampling.md`, `compose-optimize.md`, `op-win-distributed-lag.md`, `regression-model-options.md` (the two-call media-mix recipe), `compose-sweeps.md`
- Pack skills (served by `Extensions.Skills`): `contrib/mmmpulse/skills/op-reg-mmm-hill.md`, `ext-mmm-workflow.md`
- `docs/src/library/optimize.md`, `docs/src/library/mmm-pack.md`, `docs/src/library/linalg.md` (Deterministic math), `docs/src/library/compose-sweeps.md`
- Examples: `internal/examples/compose/11_mmm_decay_lhs_sweep.json`, `12_price_optimize.json`, `13_budget_allocation.json`
- `contrib/mmmpulse/README.md`
- [Model extensions](../v1.0.0-api-and-release/04-model-extensions.md) (layers: core primitives, packs, R bridge)
- [Embedder migration: Changes from U60](../v1.0.0-api-and-release/03-embedder-migration.md)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#539** public `optimize` package: `Minimize` (SLSQP, constraints, multipliers) and `LeastSquares` (bounded LM), `PULSE_OPTIMIZE_*` codes, R oracles
- [x] **#540** `MAT_SEM` and the test-only Hill regression fit through the shared solvers
- [x] **#541** `WIN_ADSTOCK` renamed `WIN_DISTRIBUTED_LAG`, no alias
- [x] **#542** an unknown window / regression / test type is one code on predict and every runtime surface (`PULSE_WINDOW_INVALID`, NEW `PULSE_REGRESSION_INVALID`)
- [x] **#543** seeded `random` / `lhs` compose sweep (`samples`, `rng_seed`, axis `range`, `sweep_seed`, `capability:compose_sweep_sampling`)
- [x] **#544** compose `optimize` block (`capability:compose_optimize`): optimum, marginals, multipliers, active bounds, multi-start, `models`
- [x] **#545** `contrib/mmmpulse` with `REG_MMM_HILL` and its skills, README and book page
- [x] **#546** `linalg/detmath` public; `WIN_DISTRIBUTED_LAG` weights and Student-t quantiles bit-identical across architectures
- [x] **#547** joint-truth fixture and the pass bar on the root library, the CLI path and `mcpserve`
- [~] **#489** (narrowed, stays open) the embedder-registered post-fit operator slot; the response-curve evaluation and budget-allocation uses it was filed for are now covered by `coef()` + `hill()` + the compose `optimize` block
- Follow-ups **#548–#560** found in this unit are filed under their owner units (see Handed on); they are not delivered here

## Decisions (owner, 2026-10-25)

- **`WIN_ADSTOCK` → `WIN_DISTRIBUTED_LAG`, no alias.** The generic lag kernel (geometric, Weibull CDF / PDF) stays in core; "adstock" is a discoverability keyword in its description and Purpose, not a name.
- **Unknown window / regression / test type: one code on predict and runtime.** `PULSE_WINDOW_INVALID` (owner `shared`), NEW `PULSE_REGRESSION_INVALID` (with `details` `{regression_index, type}`), and the TEST message aligned. The same shape on every surface.
- **The near-singular SEM `default` row is re-anchored on what the early stop determines** (converged, iterations within semopy ±1, objective 1e-7, estimates 1e-3). Evidence: 900 perturbation runs, worst estimate delta 9.49e-4, so the 1e-3 bar has about 5% headroom; raise it to ~2e-3 if a later rounding change trips it.
- **A zero sweep `rng_seed` derives from the spec only, not the data**, so predict reports the effective seed without reading records.
- **Two new feature rows**: `capability:compose_sweep_sampling` (hard edge on `capability:compose_sweep`) and `capability:compose_optimize` (hard edge on `capability:compose`).
- **Optimize block**: `direction` is required; `starts` is 1..1000, default 8; a bound is a number or a bare `params.<name>`; `coef()` reads Compose fits AND saved models (`optimize[i].models`); gradients are central finite differences.
- **`REG_MMM_HILL` standard errors are per parameter**, not all-or-nothing: a parameter on a bound, and the `k` / `s` of a zero-effect channel, are `null`; every other parameter gets its error from the inverse over the free block. This differs deliberately from `REG_OLS`, where one binding bound nulls every SE. The decay has no SE (it is not a fitted parameter): bound it by the near-best sweep spread or a bootstrap at fixed decays.
- **`internal/detmath` became public as `linalg/detmath`**, with `Lgamma` and the ziggurat added; `WIN_DISTRIBUTED_LAG` weights and the Student-t quantile chain run on it, bit-identical on arm64 and amd64. The remaining core numeric drift is a separate unit ([U61](U61-cross-arch-determinism.md)).
- **Pack acceptance.** Pass bar met at 2,000 `lhs` samples (about 400 did not hold): decay worst 0.103 (bar ±0.15), ROI 6.3% (±20%), marginal-ROI equalisation 7.8e-6, lift 13.7% (25%), 8 of 8 starts agreeing. The MCP pass adds about 22 s to `make contrib`; kept, because it is the only gate that proves the pack over `mcpserve`.
- **Packs are industry-specific; core stays general.** Recorded in [Model extensions](../v1.0.0-api-and-release/04-model-extensions.md).
- **`REG_TESTMMM_HILL` stays in root** as the contract proof (rewritten onto `optimize.LeastSquares`; its negative-effect nonnegative case now ends finite).

## Scope

**In scope**
- `optimize.Minimize` / `optimize.LeastSquares` and their oracles
- Seeded `random` / `lhs` sweep modes and the contract clean-ups the sweep needed
- The compose `optimize` block
- `contrib/mmmpulse` (`REG_MMM_HILL`) and the joint-truth acceptance fixture
- Public `linalg/detmath`

**Out of scope** (each is filed below or in `04-model-extensions.md`)
- Evolutionary and Bayesian-optimisation sweep modes (adaptive; they break the expand-then-run design)
- Binding a sweep's ranked winner as a fit; optimizer bounds computed from data aggregates; in-model adstock with sequence-aware scorers
- Nonlinear constraints with analytic gradients (finite differences only)
- Partial pooling, geo-hierarchical and Bayesian MMM
- A top-level `Optimize` operation, a `pulse optimize` CLI leaf or a `pulse_optimize` MCP tool (compose covers it)
- Changing the `MaxComposeSlots` default

## Epics & stories

### E1 — Embedder solves bounded NLS and constrained problems with `optimize/` (done)
- S1 `optimize.Minimize`, S2 `optimize.LeastSquares`, S3 R oracles and pack-author docs
### E2 — Analyst searches many parameters at once with a seeded sweep (done)
- S3 `WIN_DISTRIBUTED_LAG` rename, S4 unknown window type, S5 unknown regression / test type, S1 `random` / `lhs` sweep, S2 gates, docs and the MMM fixture proof
### E3 — Analyst optimizes an objective over fitted models in compose (done)
- S1 the block and predict validation, S2 the solve, S3 docs, guidance and examples
### E4 — MMM practitioner jointly fits curves, searches decays and reallocates budget with the pack (done)
- S1 the `contrib/mmmpulse` module and `REG_MMM_HILL`, S4 public `detmath`, S2 the pass bar, S3 pack skills, README and recipe
### E5 — Close-out and release readiness
- S1 this unit doc, the `04-model-extensions.md` amendment, TODOs, migration and release notes

## Acceptance criteria

- [x] `optimize.LeastSquares` matches `minpack.lm::nlsLM` and `optimize.Minimize` matches `nloptr` SLSQP on committed oracle problems (`TestOptimize_LeastSquaresMatchesNLSLM`, `TestOptimize_MinimizeMatchesNLoptrSLSQP`); `MAT_SEM` is unchanged on amd64
- [x] A seeded `lhs` sweep of about 2,000 samples recovers each channel's decay within ±0.15 and ROI within ±20% on the joint-truth fixture (`TestMMMPack_PassBar`)
- [x] A compose `optimize` block equalises marginal ROI across channels not at a bound and predicts lift within 25% of the true-parameter optimum (`TestMMMPack_PassBar`, `compose/13_budget_allocation.json`)
- [x] The same seed gives byte-identical results serially, in parallel and over `mcpserve` (`TestMMMPack_PassBarDeterministic`, `TestMMMPack_PassBarMCP`); arm64 and amd64 agree on the pack fit and the sweep
- [x] `random` / `lhs` sweeps and `optimize` blocks work for non-MMM users with no pack (`compose/12_price_optimize.json`)
- [x] Predict reports exact slot counts, the effective seed, and validates optimize expressions, variables and fit names without executing
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit)) — the full gate set and CI run at the PR; `status` flips to `done` at merge

## Gates & tests

- `TestPayloadSchemaGolden`, `TestPublicAPIGolden`, `TestManifest*Complete`, `TestSkillTokenBudget`, `TestSkillsCoverFeatureFences`, `TestDocsGeneratedCurrent`, `TestCodesHaveFixups`, `TestErrorOwners_Complete`, `TestFeaturesHaveSince`, `TestProfileDependenciesComplete`
- E1: `TestOptimize_*` (incl. `TestOptimize_ImportBoundary`, `TestOptimize_NoFMAContraction`, `TestOptimize_BitExact`, `TestOptimize_OracleGoldensHashed`)
- E2: the sweep sampling, `PULSE_SWEEP_*` refusal and predict-parity suites; the unknown-type parity suites for windows, regressions and tests
- E3: the optimize predict / runtime / fails-alone suites and `TestExamples_*` for the three new examples
- E4: `TestMMMPack_PassBar`, `TestMMMPack_PassBarDeterministic`, `TestMMMPack_PassBarMCP`, the pack's arm64 FMA gate, `contrib/mmmpulse` skill coverage
- Nested module: `make contrib` (runs the pack suite, about 31 s of which the MCP pass is about 22 s)

## Update Demand companions

- Skills: `compose-sweep-sampling` (new), `compose-optimize` (new), `op-win-distributed-lag` (renamed from `op-win-adstock`), `regression-model-options` (two-call recipe), `compose-sweeps`; pack skills `op-reg-mmm-hill`, `ext-mmm-workflow`
- Features: `capability:compose_sweep_sampling`, `capability:compose_optimize` (`internal/descriptor/features.go`, `Since` 1.0.0)
- Error codes: `PULSE_OPTIMIZE_INVALID` / `_NOT_CONVERGED` / `_INFEASIBLE`, `PULSE_SWEEP_MODE_MISMATCH` / `_RANGE_INVALID` / `_SAMPLES_INVALID`, `PULSE_OPTIMIZE_SPEC_INVALID` / `_VARIABLE_INVALID` / `_EXPRESSION_INVALID`, `PULSE_REGRESSION_INVALID`; `PULSE_WINDOW_INVALID` owner `shared`
- Public surface: `optimize`, `linalg/detmath` (`TestPublicAPIGolden`); `contrib/mmmpulse` nested module (`make contrib`, CI, `scripts/release-contrib.sh`, dependabot)
- Goldens regenerated with `-update` (manifest, payload schema, public API); `format_version` stays `"1.1"`; `make docs`
- [Embedder migration](../v1.0.0-api-and-release/03-embedder-migration.md) (Changes from U60)

## Handed on to later work

Filed as TODO items in [`TODO.md`](../TODO.md) (ids are TODO ids, not GitHub issues):

| Item | Owner |
|---|---|
| #489 (narrowed) embedder-registered post-fit operator slot; the curve-evaluation and allocation uses are covered by `coef()` + the `optimize` block | no unit (v1.x backlog) |
| #548 Evolutionary (CMA-ES / differential evolution) and Bayesian-optimisation sweep modes (adaptive: they break expand-then-run) | [U40](U40-compose-sweep.md) |
| #549 A sweep's ranked winner bindable as a fit, so the media-mix flow becomes one compose call | [U42](U42-fit-scoring.md) |
| #550 Optimizer bounds and constraints computed from data aggregates (callers pass literals or `params` today) | [U43](U43-post-aggregation-ratios.md) |
| #551 In-model adstock (decay estimated with an analytic SE), ordered / gap-aware rows for extensions and sequence-aware scorers | [U57](U57-transformed-predictors.md) |
| #552 Partial pooling / geo-hierarchical and Bayesian MMM as a pack on the `REG_MMM_HILL` contract | no unit (v1.x backlog) |
| #553 Nonlinear `optimize` constraints with analytic gradients (finite differences only today) | no unit (v1.x backlog) |
| #554 Remaining core cross-architecture numeric drift: audit and repo-wide gate | [U61](U61-cross-arch-determinism.md) |
| #555 Flaky CI memory-peak tests: `TestShardStream_CreateMemoryBoundedBySource/remapped`, `TestCrosstabMemory_CellChargeTable` | [U33](U33-v1-release.md) |
| #556 Glossary terms for shadow price / marginal return (they need orphan-report exemptions) | [U31](U31-guidance-guides.md) |
| #557 The `compose-requests` skill has no route to `compose-optimize` (hard budget) | [U38](U38-skill-sync.md) |
| #558 No canonical `optimize` example tag | [U38](U38-skill-sync.md) |
| #559 Extension factory refusals are runtime-only (no predict-time hook) | [U34](U34-extension-validation.md) |
| #560 `make sites` does not cover `ComposedRequest` sub-fields (`sweep`, `optimize`) | [U38](U38-skill-sync.md) |

The pre-existing mdBook warning "unclosed HTML tag `<type>`" in `docs/src/contract/payload-schema.md` found again here is already [#527](../TODO.md) (U32); not refiled.

## Release notes (v1.0.0-alpha.27)

Draft from the migration table (Changes from U60); nothing is tagged or published by this unit. Release steps: tag `v1.0.0-alpha.27` and the lockstep `contrib/mmmpulse` tag through `scripts/release-contrib.sh` (without `--push` it is a dry run; that dry run has not been exercised yet, so run it first), and edit v0.39.1 `--latest` afterwards because the release script drops the pre-release flag.

- **Behaviour changes.**
  - `WIN_ADSTOCK` is gone: it is `WIN_DISTRIBUTED_LAG` (same params, kernels and output, no alias). Replace the name in requests, templates, feature profiles and Go code; the old name is `PULSE_WINDOW_INVALID`, and a feature profile listing it is `PULSE_FEATURE_PROFILE_UNKNOWN`.
  - An unknown window type at runtime is `PULSE_WINDOW_INVALID` (was `PROCESSING_CONFIG`). An unknown regression type is NEW `PULSE_REGRESSION_INVALID` on predict and every runtime surface (was `SERVICE_VALIDATION` / `PROCESSING_CONFIG`). Predict's unknown-test-type message now reads like the runtime's and adds `details.tier`. `PULSE_WINDOW_INVALID` is now listed on every feature profile.
  - `AllSweepModes()` returns `[grid zip random lhs]`; do not iterate it to build a `for_each` mode list (a `for_each` still accepts only grid / zip). `SweepAxis.Values` is `omitempty` and no longer schema-required.
  - `WIN_DISTRIBUTED_LAG` weights and Student-t p-values, quantiles and CI bounds run on `linalg/detmath`: bit-identical across architectures, and they may move by 1 ulp on amd64 and a few ulps on arm64 (a t p-value up to about 1e-14 relative).
  - `MAT_SEM` fits through `optimize.Minimize`: on amd64 every figure is bit-identical, on arm64 the last digits can move.
  - The test-only `REG_TESTMMM_HILL` negative-effect non-negative fit now ends finite instead of on a NaN Jacobian.
- **New.**
  - Public packages `optimize` (`Minimize`, `LeastSquares`; codes `PULSE_OPTIMIZE_INVALID` / `_NOT_CONVERGED` / `_INFEASIBLE`) and `linalg/detmath`.
  - Compose `sweep` modes `random` / `lhs` with `samples`, `rng_seed`, axis `range` (`linear` / `log`) and the echoed `sweep_seed`; codes `PULSE_SWEEP_MODE_MISMATCH` / `_RANGE_INVALID` / `_SAMPLES_INVALID`; feature `capability:compose_sweep_sampling`.
  - Compose `optimize` block and `optimizations` result (multi-start SLSQP, marginals, multipliers, active bounds, `models`); codes `PULSE_OPTIMIZE_SPEC_INVALID` / `_VARIABLE_INVALID` / `_EXPRESSION_INVALID`; feature `capability:compose_optimize`.
  - Skills `compose-sweep-sampling`, `compose-optimize`, `op-win-distributed-lag`; examples `compose/11`–`13`; book pages `library/optimize.md` and `library/mmm-pack.md`.
- **`contrib/mmmpulse` (tagged alongside).** `mmmpulse.Extensions()` registers `REG_MMM_HILL` (joint nonlinear least squares of channel betas, Hill `k` / `s` and controls; `β ≥ 0` exact zeros; per-parameter standard errors; holdout, bootstrap, saved models and `ATTR_REG_PREDICT` work) and the pack skills. The stock `pulse` CLI and `pulse mcp` do not register packs: use the library or `mcpserve` with `Options.Extensions`.
- **Not in this release.** Cross-architecture drift in the rest of the core (normal quantile, chi-square prefactor, `TEST_T` inputs, regression SEs via gonum, about 20 unaudited `math.*` sites) is [U61](U61-cross-arch-determinism.md).
