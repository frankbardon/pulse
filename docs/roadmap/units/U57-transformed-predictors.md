---
id: U57
slug: transformed-predictors
title: "A regression reads a window label of its own request, so a media-mix model fits, scores a holdout and sweeps its decay in one request"
track: API & release
size: L
status: done
depends_on: [U53]
soft_depends_on: [U40, U42]
blocks: [U32]
todo_items: [490, 551]
branch: transformed-predictors
---

# U57 — transformed-predictors

**Outcome:** A regression reads a window label of its own request, so a media-mix model fits, scores a holdout and sweeps its decay in one request.

**Track:** API & release · **Size:** L · **Depends on:** [U53](U53-custom-regressions.md) · **Soft:** [U40](U40-compose-sweep.md), [U42](U42-fit-scoring.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

[U53](U53-custom-regressions.md) shipped the media-mix primitives (`WIN_ADSTOCK`, `hill()`, non-negative bounds), but a regression bound its inputs before windows ran, so an adstock had to be materialized into a derived cohort first: a two-step route. This unit (TODO #490, decided "lift it") lets a regression, a post-window formula attribute and `ATTR_REG_PREDICT` read a window label of the same request, adds an out-of-sample `holdout` to the regression, and lets a Compose sweep rank decay candidates on the held-out RMSE, with the canonical one-step decay sweep as the acceptance request on the library, CLI and MCP.

## References

- `.claude/reference/execution-modes.md` (post-window attribute phase), `predict-inspect.md`, `response-components.md`, `weighting.md`
- `internal/skills/regression-model-options.md`, `attribute-composition.md`, `compose-sweeps.md`
- [Embedder migration: Changes from U57](../v1.0.0-api-and-release/03-embedder-migration.md)

**TODO items delivered** (tick in [`TODO.md`](../TODO.md)):

- [x] **#490** Evaluate same-request window predictors for regressions; lifted for the windows-over-records shape

## Scope

**In scope**
- Window labels as regression `target`, `predictors`, `unpenalized`, robust-covariance `cluster` / `order_by` and `jackknife_by`, in the windows-over-records shape; `PULSE_REGRESSION_WINDOW_UNAVAILABLE` for the grouped shape; predict and runtime agree
- The post-window attribute phase: a row-local `ATTR_FORMULA` / `ATTR_REG_PREDICT` reading a window label runs after the windows; every other attribute reading one is `PULSE_ATTRIBUTE_WINDOW_ORDER`
- NaN / ±Inf regression inputs drop listwise like null
- `RegressionSpec.Holdout` / `RegressionResult.Holdout` and `capability:regression_holdout`, composing with bounds, penalty, resampling, selection, weights and extension scorers
- The one-step MMM decay sweep through Compose fits / sweep / rank on every surface, the skills, the explain step order and the `compose/08_mmm_decay_sweep.json` example over the `mmm_weekly` fixture

**Out of scope**
- Re-running windows per bootstrap replicate (windows are computed once; documented)
- A window label in a grouped, aggregated or crosstab request, or as a slot `weight` (materialize first)

## Epics & stories

### E1 — Regress directly on a window output
- S1 admit window labels (predict and runtime), S2 parity, S3 non-finite inputs drop listwise
### E2 — Saturate an adstock in the same request
- S1 the post-window attribute phase, S2 the chain parity and Compose fits / sweep
### E3 — Score a holdout and rank a decay sweep on it
- S1 `holdout`, S2 its composition with the model options, S3 the canonical sweep on every surface
### E4 — Agents and embedders can learn it
- S1 skills and the explain step order, S2 the example and the roadmap close-out

## Acceptance criteria

- [x] A regression fits on a window label identically to materialize-then-regress; a grouped shape is refused with a coded error on predict and runtime
- [x] A saturation attribute over an adstock feeds a regression in one request, bit-identical to the two-step route
- [x] `holdout` figures equal a filter-split fit scored with `ATTR_REG_PREDICT` and the RMSE / MAE aggregators
- [x] The decay sweep ranks the true decay first on the library, CLI and MCP
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestWindowPredictor_*`, `TestHoldout_*`, `TestExamples_MMMDecaySweepRanksTrueDecayFirst` (later replaced by `TestExamples_ComposeRunClean`, mmm-followups E3-S6), `TestExplain_PostWindowAttributeOrder`
- `TestPayloadSchemaGolden`, `TestPublicAPIGolden`, `TestCodesHaveFixups`, `TestErrorOwners_Complete`, `TestSkillTokenBudget`

## Update Demand companions

- Skills: the window `op-win-*` skills, `regression-model-options`, `compose-sweeps`, `compose-fits`, `attribute-composition`, `op-reg-ols`, `derived-cohorts`
- Goldens regenerated with `-update`; `format_version` stays `"1.1"`
- [Embedder migration](../v1.0.0-api-and-release/03-embedder-migration.md) (Changes from U57)

## Human inputs & decisions

- [x] Decided (U53 owner decision 10): lift the restriction for the windows-over-records shape; the grouped shape stays refused with a coded error.
