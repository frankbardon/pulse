---
id: U63
slug: mmm-followups
title: "A media-mix run is one Compose call on the stock CLI, and a request aggregates window-derived columns"
track: Vector & matrix
size: L
status: in-progress
depends_on: []
soft_depends_on: [U40, U53, U57, U60, U62]
blocks: [U32]
todo_items: [549, 297, 570, 571, 572, 573, 574, 575, 576, 577, 578, 579, 580, 581]
branch: mmm-followups
---

# U63 — mmm-followups

**Outcome:** A media-mix run is one Compose call on the stock CLI, and a request aggregates window-derived columns.

**Track:** Vector & matrix · **Size:** L · **Depends on:** — · **Soft:** [U40](U40-compose-sweep.md), [U53](U53-custom-regressions.md), [U57](U57-transformed-predictors.md), [U60](U60-mmm-estimation.md), [U62](U62-bayesian-mmm.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

Verifying the media-mix example on `v1.0.0-alpha.28` found six warts. Each had a workaround; together they forced embedders into label parsing, a second templated call, client-side totals, a mirrored classifier and Go-only registration. This unit removes them.

- **CLI fixes (E1).** An old `WIN_ADSTOCK` request is told the new name (`details.renamed_to`). `--limit name=value` is a root persistent flag, so `pulse --limit max_compose_slots=2001 api compose ...` runs; `pulse recommend --limit` is now `--top`.
- **Built-in MMM regressions (E2).** `REG_MMM_HILL` and `REG_MMM_BAYES` are built-in regressions (`internal/regmmm`, registered through `internal/regadapt`), so the stock `pulse` CLI and `pulse mcp` run them. Predict raises their spec refusals. The skill `ext-mmm-workflow` became the core `mmm-workflow`. `contrib/mmmpulse` is removed, with no shim.
- **Sweep winner bound as a fit (E3).** Rankings carry the slot and the typed axis values (`RankEntry.Slot`, `Axes`). A `fits[].from_sweep` entry binds the rank-1 slot as a regression fit, and `sweep_axis("fit", "axis")` reads the winner's axis value, so sweep, fit, contributions and `optimize` are one call. Unseeded draws now ignore JSON key order. The MMM tests were cut to operator level (E3-S6).
- **Windows-first aggregation (E4).** One classifier (`vectors.WindowsFirst`) decides that a request aggregating a window column, or a post-window attribute label, runs its windows over the record rows first; ungrouped, grouped and crosstab shapes agree with a cohort that stores the column. Predict reports the plan as `post_window`. The skill `windows-first` carries a contribution-by-quarter recipe.
- **Close-out (E5).** This unit doc, the migration rows, the release notes below and the deferral TODOs. The unreproducible calibration and accuracy figures were deleted from the book page and skills.

Status: implemented on branch `mmm-followups`; targets `v1.0.0-alpha.29`. Nothing is released or tagged by this unit's close-out.

## References

- `.claude/reference/update-demand.md` (compose, regression, windows slices), `execution-modes/compose.md`, `execution-modes/filters-and-expressions.md` (Windows-first), `predict-inspect.md`, `architecture/packages.md`
- `docs/src/library/mmm.md`, `docs/src/library/compose-sweeps.md`
- Skills: `mmm-workflow`, `compose-sweep-bind`, `windows-first`, `op-reg-mmm-hill`, `op-reg-mmm-bayes`
- [Embedder migration: Changes from U63](../v1.0.0-api-and-release/03-embedder-migration.md)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#549** A sweep's ranked winner bindable as a fit (E3-S3)
- [x] **#297** A later request references a sweep winner instead of typing the axis values (E3-S1)
- Follow-ups **#570–#581** found in this unit are filed under their owner units (see Handed on); they are not delivered here

## Decisions (owner)

- **Operator-level tests only.** Pulse tests individual operations, not MMM workflows: the one-call recipe is documentation, and the pass bars, coverage study and MMM sweep suites were removed (E3-S6).
- **Unreproducible figures are deleted.** A calibration or accuracy number without a harness is not published (the 50-replicate coverage figures, the sweep-sample-count note and the one-time accuracy figures).
- **Seed key-order bug is fixed in this effort** (E3-S7): every rng_seed 0 derivation hashes the body in one canonical form.
- **The catalog split limit and the crosstab fusion bugs are fixed in this effort** (E4-S6), not deferred.
- **A window param bound to the sweep winner is a design question**, filed as a TODO; the shipped recipe rebuilds adstock with `for_each` `WIN_LAG` plus a Horner formula over `sweep_axis`.
- **CI speed is a separate effort**, requested by the owner (below).

## Scope

**In scope**
- The two CLI fixes, the two built-in MMM regressions and the module removal
- `from_sweep`, `sweep_axis`, ranking slot and axes
- Windows-first aggregation for every shape and predict's `post_window`
- Docs, skills, migration rows, release notes and deferral TODOs

**Out of scope** (each filed below)
- Objective helper, temporal train/test split, synth `lag()` partition, a standalone public classifier
- Retiring `otelpulse` / `prompulse`, the CI speed effort
- A window param bound to the sweep winner, extension `FieldInputs` windows-first, widening windows to categorical sources

## Epics & stories

### E1 — CLI fixes (done)
- S1 the `WIN_ADSTOCK` hint, S2 `--limit` on every leaf and `recommend --top`
### E2 — MMM regressions in the stock CLI and MCP server (done)
- S1 `REG_MMM_HILL`, S2 `REG_MMM_BAYES`, S3 `contrib/mmmpulse` removed
### E3 — Sweep winner bound as a fit, one-call MMM (done)
- S1 ranking fields, S2 `from_sweep` static, S3 runtime binding, S4 `sweep_axis`, S5 the recipe, S6 operator-level tests, S7 canonical seeds
### E4 — Windows-first aggregation and `post_window` (done)
- S1 classifier, S2 ungrouped, S3 grouped and crosstab, S4 predict `post_window`, S5 docs, S6 catalog split and crosstab fixes
### E5 — Close-out
- S1 this unit doc, migration rows, release notes, deferral TODOs

## Acceptance criteria

- [x] `pulse --limit max_compose_slots=2001 api compose` runs; a bad `--limit` exits 2 on every leaf
- [x] `REG_MMM_HILL` / `REG_MMM_BAYES` run in the stock CLI and `pulse mcp` with no registration; `contrib/mmmpulse` is gone
- [x] A compose with a ranked sweep and `fits[].from_sweep` runs sweep, bound fit, explicit slots and `optimize` in one call; `ranking[]` entries carry `slot` and typed `axes`
- [x] A request aggregating window columns runs (ungrouped, grouped, crosstab) and equals the same request over a stored column; predict reports `post_window`
- [x] Migration rows, release notes and deferral TODOs are filed; `#549` and `#297` are ticked
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit)) — the full gate set and CI run at the PR; `status` flips to `done` at merge

## Gates & tests

- `TestPayloadSchemaGolden`, `TestPublicAPIGolden`, `TestManifest*Complete`, `TestSkillTokenBudget`, `TestSkillsCoverFeatureFences`, `TestDocsGeneratedCurrent`, `TestFeaturesHaveSince`, `TestProfileDependenciesComplete`
- E3: `TestComposeSweepAxis_*`, the `from_sweep` predict / runtime parity suites
- E4: the windows-first classifier, engine and predict `post_window` suites, the example-corpus byte-identity gate
- Operator-level only: `internal/regmmm` keeps the model suites; there is no MMM workflow test

## Update Demand companions

- Skills: `mmm-workflow` (renamed), `op-reg-mmm-hill`, `op-reg-mmm-bayes`, `compose-sweep-bind` (new), `windows-first` (new), `compose-sweeps`, `compose-fits`, `tool-predict`, `crosstab-guide`
- Features: `capability:compose_sweep_bind`, `REG_MMM_HILL`, `REG_MMM_BAYES` (`internal/descriptor/features.go`, `Since` 1.0.0)
- Public surface: `types.RankEntry` fields, `types.FitFromSweep`, `descriptor.PostWindowPlan` (`TestPublicAPIGolden`)
- Reference docs: `update-demand/compose-sweep-bind.md` (new slice), `execution-modes/compose.md`, `execution-modes/filters-and-expressions.md`

## Handed on to later work

Filed as TODO items in [`TODO.md`](../TODO.md) (ids are TODO ids, not GitHub issues):

| Item | Owner |
|---|---|
| #570 Optimizer objective helper `response(fit, channel, spend)` | [U60](U60-mmm-estimation.md) |
| #571 Temporal mode for `FEAT_TRAIN_TEST_SPLIT` (`holdout` substitutes today) | [U57](U57-transformed-predictors.md) |
| #572 Synth `lag()` partition argument (no geo synth yet) | [U58](U58-expr-math-lags-params.md) |
| #573 A standalone public post-window classifier (predict's `post_window` needs a cohort header) | [U35](U35-predict-runtime-parity.md) |
| #574 Retire the `otelpulse` / `prompulse` contrib modules and `scripts/release-contrib.sh` lockstep tags (root-deps tension) | [U01](U01-release-pipeline.md) |
| #575 CI wall-clock cut to 8 minutes or less (aim 6), separate effort | no unit (separate effort) |
| #576 A window param bound to the sweep winner | [U58](U58-expr-math-lags-params.md) |
| #577 Extension aggregator `FieldInputs` reading a window label turns windows-first on | [U34](U34-extension-validation.md) |
| #578 `TestMMMBayes_HealthWarnings` doctored-sampler subtests are seed-fragile | [U62](U62-bayesian-mmm.md) |
| #579 A sweep body whose `models` paths vary by axis is untested for a bound fit | [U40](U40-compose-sweep.md) |
| #580 A parallel non-FailFast phase-0 failure aggregates only phase-0 failures: document it | [U40](U40-compose-sweep.md) |
| #581 Widen `WIN_LAG` / `WIN_LEAD` to categorical sources | [U57](U57-transformed-predictors.md) |

## Release notes (v1.0.0-alpha.29)

Draft from the migration table (Changes from U63); nothing is tagged or published by this unit. Release steps: tag `v1.0.0-alpha.29`; the lockstep contrib tags are `contrib/otelpulse` and `contrib/prompulse` only (there is NO `contrib/mmmpulse` tag) through `scripts/release-contrib.sh` (dry run without `--push` first); edit v0.39.1 `--latest` afterwards because the release script drops the pre-release flag. Only after human approval.

- **Behaviour changes.**
  - `contrib/mmmpulse` is removed, with no shim and no `v1.0.0-alpha.29` tag. Drop the import, its `go.mod` require and `mmmpulse.Extensions()` from `pulse.Options.Extensions`; `REG_MMM_HILL` and `REG_MMM_BAYES` are built-in. An embedder registering either name itself is refused (`PULSE_EXTENSION_NAME_COLLISION`).
  - `REG_MMM_HILL` / `REG_MMM_BAYES` are built-in, so the manifest lists them under `regressions` (not `extensions.regressions`) and a feature profile can hide them. The skill `ext-mmm-workflow` is renamed `mmm-workflow`.
  - Unseeded draws change. At `rng_seed` 0 a seed derives from a canonical form of the body, so draws no longer depend on JSON key order. A sweep, bootstrap or Bayesian slot whose keys were not already sorted at every depth gets new draws; pin an `rng_seed` to keep an old run (a sweep echoes `sweep_seed`).
  - `types.RankEntry` gains `Slot` and `Axes`, so it is no longer comparable: `==` and `slices.Equal` on it no longer compile. Use `reflect.DeepEqual`.
  - `pulse recommend --limit N` is now `--top N`; `--limit name=value` is the root resource-limit flag on every leaf, and a bad `--limit` exits 2 (it was 1 on `pulse mcp`).
  - A missing `params.time` on `REG_MMM_BAYES` is `PROCESSING_CONFIG` (was `PULSE_REQUEST_PARAM_INVALID`). A duplicate predictor on it is refused. Predict now raises both models' spec refusals with the runtime's code, message and details.
  - `sweep_axis` is a reserved function name: an embedder `ExprFunction` of that name is refused (`PULSE_EXTENSION_NAME_RESERVED`).
  - The strict JSON decoder (`pulse api`, MCP) reports an unknown or hidden key on a `fits` entry with `details.fit`.
  - A recommend draft puts a regression's non-slot params under `params`, and `REG_MMM_HILL` is now a `recommend` driver candidate.
  - `WIN_ADSTOCK` is still refused (no alias) but the message names `WIN_DISTRIBUTED_LAG` and details carry `renamed_to`.
  - A crosstab carrying a window that no axis, cell or margin reads is refused (`PULSE_WINDOW_INVALID` `crosstab_window_unread`); it used to drop the window silently.
  - A crosstab with `regressions` now runs buffered and fits them (it was `PROCESSING_INTERNAL` on the fusable path and dropped the regressions on the buffered path); a historical-order crosstab with windows is reported buffered by predict.
  - A request-level `weight` on a windows-first request is refused (`PROCESSING_CONFIG`, no weighted window form); put the weight on aggregation slots.
  - `PULSE_LIMIT_EXCEEDED`'s message and fixup name the root `--limit` flag.
- **New.**
  - `fits[].from_sweep` binds the winning sweep slot as a fit, and `sweep_axis("fit", "axis")` reads its axis values: sweep, bound fit, contributions and `optimize` in one Compose call. Feature `capability:compose_sweep_bind`; skill `compose-sweep-bind`.
  - `ranking[]` entries carry `slot` and typed `axes`.
  - Windows-first aggregation: aggregations, groupers and crosstab axes over window labels (ungrouped, grouped, crosstab) run in one request; skill `windows-first`, examples `windows/15` and `windows/16`.
  - Predict reports `post_window` (`PredictResult.PostWindow`, `ComposeSlotPredict.PostWindow`): which attributes run after windows, whether windows run first, by attribute index, even for refused drafts.
  - `pulse --limit name=value` on any leaf, with shell completion.
  - The media-mix design guide as one compose call (`mmm-workflow`, book page `library/mmm.md`).
- **Not in this release.** The items under Handed on: an optimizer objective helper, temporal train/test split, synth `lag()` partition, a standalone classifier, contrib retirement, the CI speed effort, a window param bound to the sweep winner, extension `FieldInputs` windows-first and categorical windows (#570–#581).
