---
id: U62
slug: bayesian-mmm
title: "A media-mix practitioner fits a Bayesian model and gets return-on-spend intervals that include adstock uncertainty"
track: Vector & matrix
size: L
status: in-progress
depends_on: []
soft_depends_on: [U60]
blocks: [U32]
todo_items: [552, 561, 562, 563, 564, 565, 566, 567, 568]
branch: bayesian-mmm
---

# U62 — bayesian-mmm

**Outcome:** A media-mix practitioner fits a Bayesian model and gets return-on-spend intervals that include adstock uncertainty.

**Track:** Vector & matrix · **Size:** L · **Depends on:** — · **Soft:** [U60](U60-mmm-estimation.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

[U60](U60-mmm-estimation.md) shipped `REG_MMM_HILL`, whose decay is a sweep axis: the betas, `k` and `s` are decay-conditional and the decay has no interval. This unit adds the Bayesian route, where raw spend is adstocked inside the model and the decay is a posterior parameter, so every interval carries it.

- **Sampler (E1).** A public `mcmc` package: `Model` / `Sampler`, the `Positive` / `Unit` / `Bounded` transforms, rank-normalised split R-hat and bulk / tail ESS, and a seeded multinomial NUTS (Stan-style warmup, windowed diagonal metric). Byte-identical across runs, worker counts and architectures (confirmed on CI amd64). Codes `PULSE_MCMC_INVALID` / `PULSE_MCMC_FAILED`.
- **Extension contract (E2).** Three additive `extend` additions: `RegressionOutput.Warnings` (coded, lifted to `RegressionResult.Warnings` and the envelope, escalated by `Strict`; the new code `PROCESSING_REGRESSION_DIAGNOSTIC`), `ContextRegression.FitContext` (a caller cancel and `RequestTimeout` reach a long fit), and `RegressionRegistration.NoResample` (refused identically on predict and at runtime; the resample keys moved to the `opt_out` field class).
- **`REG_MMM_BAYES` (E3).** In `contrib/mmmpulse`: geometric adstock (`max_lag` 13, `normalize: false`), Hill, linear controls and Normal noise, with ROI priors by default, internal max-abs scaling and a MAP warm start. Outputs: ROI, analytic mROI and contribution intervals, per-coefficient summaries, sampler diagnostics and a model-owned holdout. A sampler fault is reported as a coded warning, never refused. Pass bar on the joint fixture: all 4 channels covered, max R-hat 1.0037, min bulk ESS 1754, 0 divergences, median ROI error at most 1.4%.
- **Validation and docs (E4).** A coverage study outside CI (its figures were removed by mmm-followups: no harness reproduces them), the `op-reg-mmm-bayes` skill, the Bayesian-vs-sweep route guide with the lift-test-prior recipe, and this close-out.

Status: implemented on branch `bayesian-mmm`; targets `v1.0.0-alpha.28` with a lockstep `contrib/mmmpulse` tag. Nothing is released or tagged by this unit's close-out. *(Retired in `v1.0.0-alpha.29` by mmm-followups: `REG_MMM_HILL` and `REG_MMM_BAYES` are built-in regressions in `internal/regmmm`, and the `contrib/mmmpulse` module is removed.)*

## References

- `.claude/reference/update-demand.md` (regression, error-code and extension slices), `architecture.md` (packages, extension surface), `response-components.md` (per-slot failures), `predict-inspect.md`, `matrix-and-vectors.md` (FMA policy)
- `docs/src/library/mcmc.md`, `docs/src/library/mmm-pack.md`, `docs/src/internals/adding-regression.md`, `docs/src/internals/extension-points.md`
- Pack skills: `contrib/mmmpulse/skills/op-reg-mmm-bayes.md`, `ext-mmm-workflow.md`
- [Embedder migration: Changes from U62](../v1.0.0-api-and-release/03-embedder-migration.md)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [~] **#552** (narrowed, stays open) the Bayesian MMM part is done; the geo hierarchy stays open as #561
- Follow-ups **#561–#569** found in this unit are filed under their owner units (see Handed on); they are not delivered here

## Decisions (owner)

- **Decay is in the posterior.** Adstock runs inside the model, the only honest ROI coverage, at the cost of an order-dependent regression with no row scoring.
- **The sampler is a core package, the model is a pack.** `mcmc` knows nothing about media mix; NUTS sits behind a public `Sampler` interface so more samplers can be added.
- **ROI priors by default** (LogNormal(0.2, 0.9)), with a per-channel `beta` option (HalfNormal(1)); a lift test becomes an informative ROI prior, with the estimand-mismatch caveat (window, geography, incrementality definition) documented.
- **Decay prior Beta(1, 1)**, not Beta(1, 3): the latter pulls toward fast decay and biases a long-memory channel's ROI on the ridge decay shares with `k` and the effect. Hill `s` is LogNormal(0.3, 0.5), `k` is TruncNormal(1, 1, 0.02, 10) on the peak-scaled adstock.
- **Defaults** 4 chains x 1000 warmup x 1000 draws with hard caps (chains 16, warmup and draws 20,000); `target_accept` 0.8; seed from `rng_seed`, or derived from the spec only (never data or clock).
- **Sampler health is reported, not refused:** `PROCESSING_REGRESSION_DIAGNOSTIC` with `details.reason` `rhat_high` / `ess_low` / `divergences` / `treedepth_saturated`; `Strict` fails the slot with that code. A 2 x 100-draw run always warns `ess_low`.
- **No scoring.** `REG_MMM_BAYES` has no Scorer: engine `holdout`, `save`, `ATTR_REG_PREDICT` and `resample` are refused; the holdout is model-owned (`params.holdout_from`).
- **A required `params.time`:** contiguous integers, step 1; a weekly date column is `time_gap`, a panel is `duplicate_time`.
- **mROI is analytic** (curve held fixed). Intervals are posterior quantiles in `Outputs` at `credible_level` (default 0.9).
- **Cancellation is a fix, not a deferral:** `ContextRegression.FitContext`. Resample is refused declaratively through `NoResample`.
- **Scope cut:** geo hierarchy, other kernels and curves, an in-model baseline, a posterior optimizer, a lift likelihood and raw draws are out (filed below).

## Scope

**In scope**
- The public `mcmc` package and its R-hat / ESS oracles
- The three `extend` additions and their predict / runtime parity
- `REG_MMM_BAYES`, its pass bar and the coverage study
- Pack skill, route guide, README and migration notes

**Out of scope** (each filed below)
- Geo-hierarchical pooling, other kernels and saturation curves
- A lift-test likelihood, a posterior-aware optimizer, raw draws
- Save / scoring, other samplers

## Epics & stories

### E1 — Embedder samples any model with seeded NUTS (done)
- S1 the `mcmc` contract, transforms and diagnostics, S2 NUTS, S3 example, library page and migration note
### E2 — An extension regression can warn, be cancelled and refuse resample (done)
- S1 coded warnings, S2 `FitContext` and `NoResample`
### E3 — A practitioner fits `REG_MMM_BAYES` and gets ROI intervals (done)
- S1 model maths, S2 registration, fit and `PULSE_MCMC_*` codes, S3 outputs, S4 the pass bar
### E4 — Validated, documented, release-ready
- S1 the coverage study, S2 skill, route guide and README, S3 this unit doc, TODOs, migration and release notes

## Acceptance criteria

- [x] An embedder samples a custom model with `mcmc` NUTS; the same seed gives the same bytes across runs, workers and architectures (CI amd64)
- [x] An extension regression returns coded warnings that reach the result and the envelope, escalated by `Strict`; honours ctx; and refuses `resample` identically on predict and at runtime
- [x] `REG_MMM_BAYES` on the joint fixture covers all 4 channels, max R-hat <= 1.01, min ESS >= 400, 0 divergences, median ROI error <= 20% (met: 1.0037, 1754, 0, 1.4%), with a pinned golden hash
- [x] A coverage study ran outside CI (removed by mmm-followups E3-S6; no calibration figure is published)
- [x] Skill, route guide, lift-test recipe, README and book page document the route, defaults and caveats
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit)) — the full gate set and CI run at the PR; `status` flips to `done` at merge

## Gates & tests

- `TestPayloadSchemaGolden`, `TestPublicAPIGolden`, `TestManifest*Complete`, `TestCodesHaveFixups`, `TestErrorOwners_Complete`, `TestDocsGeneratedCurrent`, `TestClaudeMdSizeBudget`
- E1: `TestMCMC_*`, `TestNUTS_*` (import boundary, FMA contraction, byte identity)
- E2: the warnings lift, `FitContext` and `NoResample` predict / runtime parity suites
- E3: `TestMMMBayes_*`, the pass bar and its golden hash `testdata/bayes_passbar.sha256`
- E4: `TestMMMBayes_CoverageStudy` (build tag `mmmcoverage`, outside CI), `contrib/mmmpulse` skills test
- Nested module: `make contrib`
- Later (mmm-followups E3-S6): the pass bar, its hash and the coverage study harness were removed — Pulse tests operators one operation at a time. `REG_MMM_BAYES` keeps its operator tests in `internal/regmmm` and its figures were deleted from the docs (an unreproducible number is not published).

## Update Demand companions

- Skills: pack skills `op-reg-mmm-bayes` (new), `ext-mmm-workflow`
- Error codes: `PROCESSING_REGRESSION_DIAGNOSTIC`, `PULSE_MCMC_INVALID`, `PULSE_MCMC_FAILED` (fixups and owners)
- Public surface: `mcmc` and the three `extend` additions (`TestPublicAPIGolden`); `contrib/mmmpulse`
- Reference docs and CLAUDE.md: `regression` warnings clause in "Output Format Contract"; `architecture/packages.md` lists `mcmc`

## Handed on to later work

Filed as TODO items in [`TODO.md`](../TODO.md) (ids are TODO ids, not GitHub issues):

| Item | Owner |
|---|---|
| #552 (narrowed) geo-hierarchical MMM; the Bayesian part is done | no unit (v1.x backlog) |
| #561 Geo-hierarchical MMM (partial pooling across markets) | no unit (v1.x backlog) |
| #562 Weibull and delayed adstock kernels for `REG_MMM_BAYES` | no unit (v1.x backlog) |
| #563 Saturation curves other than Hill | no unit (v1.x backlog) |
| #564 A lift-test likelihood (an experiment as data, not only a prior) | no unit (v1.x backlog) |
| #565 A posterior-aware budget optimizer | no unit (v1.x backlog) |
| #566 Raw posterior draws on the wire | no unit (v1.x backlog) |
| #567 Save / scoring with pre-adstocked inputs | no unit (v1.x backlog) |
| #568 Samplers other than NUTS | no unit (v1.x backlog) |
| #569 Cross-arch determinism of `internal/mmmfixture` (flagged lines `joint.go:198`, `~296-307`, `joint_test.go:69`; no FMA gate on the package) | [U61](U61-cross-arch-determinism.md) |

## Release notes (v1.0.0-alpha.28)

Draft from the migration table (Changes from U62); nothing is tagged or published by this unit. Release steps: tag `v1.0.0-alpha.28` and the lockstep `contrib/mmmpulse` tag through `scripts/release-contrib.sh` (run the dry run without `--push` first), and edit v0.39.1 `--latest` afterwards because the release script drops the pre-release flag. Only after human approval.

- **Behaviour changes.**
  - Every shaped response's `returned.digest` (`rp1:...`) moves, because the new nested regression `warnings` keep-path joins the hashed plan. The shaped bytes are otherwise unchanged. Never pin a digest across releases.
  - An extension regression's `resample`, `jackknife_groups`, `jackknife_by` and `bootstrap_iters` keys are now field class `opt_out` (was `accepted`) in `descx.RegressionFieldClassOf`. A model that sets `NoResample` refuses them as `PROCESSING_CONFIG` on predict and at runtime; a model that does not is unchanged.
  - New error codes `PROCESSING_REGRESSION_DIAGNOSTIC`, `PULSE_MCMC_INVALID` and `PULSE_MCMC_FAILED`.
- **New.**
  - Public package `mcmc`: `Model`, `Sampler`, `NUTS`, the transforms, `RHat` / `ESSBulk` / `ESSTail`; seeded and byte-identical across runs, workers and architectures. Book page `library/mcmc.md`.
  - `extend`: `RegressionOutput.Warnings` (lifted to `RegressionResult.Warnings` and the envelope; `Strict` fails the slot with the warning's code), `ContextRegression.FitContext`, `RegressionRegistration.NoResample`.
- **`contrib/mmmpulse` (tagged alongside).** `REG_MMM_BAYES`: raw spend adstocked in the model, Hill saturation, NUTS posterior; ROI, analytic mROI and contribution intervals, diagnostics, model-owned holdout. Needs `params.time` as contiguous integers. About 32-39 s per default fit. New pack skill `op-reg-mmm-bayes`.
- **Not in this release.** Geo hierarchy, other kernels and curves, a lift likelihood, a posterior optimizer, raw draws, save / scoring and other samplers (#561-#568); `internal/mmmfixture` FMA hygiene is [U61](U61-cross-arch-determinism.md) (#569).
