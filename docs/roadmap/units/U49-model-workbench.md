---
id: U49
slug: model-workbench
title: "Models fit reliably and are validated with standard metrics, and leakage-safe encoding is one parameter"
track: Statistical integrity
size: L
status: done
depends_on: []
soft_depends_on: [U42]
blocks: [U32]
todo_items: [339, 340, 341, 342, 343, 344, 345, 346, 347, 395, 600]
branch: model-workbench
---

# U49 — model-workbench

**Outcome:** Models fit reliably and are validated with standard metrics, and leakage-safe encoding is one parameter.

**Track:** Statistical integrity · **Size:** L · **Depends on:** none · **Soft:** [U42](U42-fit-scoring.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

The credit example found a fitting bug and several missing pieces. `REG_GLM` binomial fails on a strong but not separated binary predictor (2% vs 93% default) with `PROCESSING_REGRESSION_RANK_DEFICIENT`: IRLS starts from the global mean and takes full Newton steps with no step-halving, so the linear predictor diverges; R's `glm` converges on the same data. The error blames collinearity, and one failing regression slot fails the whole request. There is no penalised GLM, no AUC, and the leave-one-out jackknife is unusable at 42,000 rows. The documented leakage-safe alternative to `FEAT_TARGET_ENCODE` means transcribing 40 per-category means into a formula. Decile labels sort D1, D10, D2.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([credit risk model](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/credit-risk-model/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `skills/regression-modeling.md`, `skills/op-reg-glm.md`, `skills/op-reg-mod-resample.md`, `skills/feature-engineering.md`, `skills/op-feat-target-encode.md`
- `.claude/reference/guided-analysis.md`, `.claude/reference/weighting.md` (new operators are weight-aware or refuse)
- [U36](U36-reference-oracles.md) (GLM oracle cases), [U42](U42-fit-scoring.md) (applying a fit to rows)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#339** (Model workbench) `REG_GLM` fits robustly: per-row starting values (`mustart`) and step-halving on deviance increase; a distinct coded error for divergence or separation instead of `RANK_DEFICIENT`; an oracle case on the showcase's `default ~ collections_flag` against R
- [x] **#340** (Model workbench) A failing regression slot reports its own error without discarding the request's other results (decision below)
- [x] **#341** (Model workbench) Penalised GLM (`l2`, and `l1` / `elasticnet`) for the binomial and Poisson families
- [x] **#342** (Model workbench) Discrimination metrics: a weight-aware AUC / Gini aggregator over a score and a binary outcome, and a share-of-partition window for cumulative capture curves
- [x] **#343** (Model workbench) A grouped (k-fold or delete-d) jackknife, so resampled standard errors are practical at tens of thousands of rows
- [x] **#344** (Model workbench) Leakage-safe encoding in one parameter: `FEAT_TARGET_ENCODE` `fit_on` (category means from the training rows, applied to all) or out-of-fold encoding
- [x] **#345** (Model workbench) `ATTR_REG_PREDICT` (U42) scores GLM families on the response scale and can fit on a subset (training rows) while scoring all rows. **Delivered by [U42](U42-fit-scoring.md) (E1-S2):** `scale: response` (the default) applies the inverse link and `scale: link` returns the linear predictor; a compose `fits` entry's own filter picks the training rows while a slot scores every row
- [x] **#346** (Model workbench) `GROUP_QUANTILE` and `GROUP_RANGE` keys sort in bucket order (D01…D10, numeric ranges), not lexicographically
- [x] **#347** (Model workbench) Acceptance: the credit example demonstrates leakage with the binary `collections_flag`, reports AUC, uses train-only target encoding, and scores the holdout with no transcribed coefficient

## Scope

**In scope**
- GLM robustness and per-slot errors
- Penalised GLM
- AUC/Gini and capture curves
- Grouped jackknife
- Train-only target encoding
- GLM scoring requirements for U42 (delivered by U42 as #345)
- Bucket key order

**Out of scope**
- New model families (trees, boosting)
- Hyperparameter search beyond U40's sweep

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(model-workbench/E<n>-S<m>): …`; close each epic with `milestone(model-workbench/E<n>): vertical slice complete — <epic title>`.

### E1 — Fits that converge
- S1: IRLS starting values and step-halving; divergence code; oracle case
- S2: per-slot regression errors

### E2 — Validation metrics
- S1: AUC/Gini aggregator; share-of-partition window
- S2: bucket key order

### E3 — Leakage-safe pipelines
- S1: penalised GLM; grouped jackknife
- S2: train-only target encoding; convert the credit example, scoring its holdout with `ATTR_REG_PREDICT` (GLM scoring itself shipped in U42, #345)

## Acceptance criteria

- [x] `default ~ collections_flag` converges and matches R's coefficients within oracle tolerance (E1-S1, `glm_irls.json`)
- [x] AUC on the holdout matches a reference implementation — in repo on the committed credit-shaped fixture against pROC (E3-S4, `TestCreditWorkbench_EndToEnd`, `credit_workbench.json`); the casebook showcase holdout re-run is E3-S5
- [x] The credit README's requests 11–14 contain no copied numbers — [pulse-casebook#6](https://github.com/frankbardon/pulse-casebook/pull/6) (E3-S5): purpose encoding is one `FEAT_TARGET_ENCODE` `fit_on`/`folds`, holdout scored by Compose `fits` + `ATTR_REG_PREDICT`
- [x] Showcase examples simplified where this unit allows (credit risk model): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse — [pulse-casebook#6](https://github.com/frankbardon/pulse-casebook/pull/6) (E3-S5); found and fixed the Compose `explain` fit-binding bug (E3-S6)
- [x] `format_version` stays `"1.1"`; every wire change is additive; the deliberate behaviour breaks are listed in [03-embedder-migration](../v1.0.0-api-and-release/03-embedder-migration.md) "Changes from U49"
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit)) — the casebook conversion (E3-S5) lands in its own repository

## Gates & tests

- `TestSkillsCoverAllRegressions`, the regression reference oracles, `TestEveryOperatorHasAnExampleTag`, `TestSkillsCoverAllPurposes`, `TestFeaturesHaveSince`
- New: GLM non-convergence regression test, AUC oracle, target-encode train-only leakage test, grouped-jackknife oracle
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- Atomic skills for every new operator, `capabilities_*.go`, example tags, `Purpose` / `Interpretation`, `features.go`, weight class in `weighting.md`, `make docs`
- `skills/regression-modeling.md` gotchas (the misleading remedy for divergence), `skills/feature-engineering.md` (the leakage trap now has a parameter)
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Inherited from U42

Filed from [U42 handed on](U42-fit-scoring.md#handed-on) and moved here from [U46](U46-grouped-analysis.md) (U46 E4-S3): a numeric-range grouper is the train/holdout split a model workbench needs. Tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#395** — A grouper over explicit numeric ranges (e.g. 0–90 and 91–103), so a train/holdout split that is not a multiple of the `GROUP_RANGE` interval is one slot instead of a filter or two slots. Found in U42 (E2-S1).

## Human inputs & decisions

- **Decided:** partial success is the DEFAULT for a failed regression, test or matrix slot and a failed Compose fit (`types.SlotError` on the result, `PULSE_SLOT_FAILED` warning); `Options.Strict` keeps fail-fast with the slot's own code. Configuration faults stay fatal.
- **Decided:** AUC is an aggregator (`AGG_AUC` over a score and a 0/1 outcome, per group), with a DeLong interval that is `null` under a weight; lift is a dedicated `WIN_LIFT`, a general `WIN_FORMULA` is v1.x backlog (#479).
- **Decided:** one penalty objective across `REG_OLS` and `REG_GLM` — glmnet's with `standardize = FALSE`. OLS `l2` already was it; OLS `l1` / `elasticnet` were standardized and now are not (the behaviour break), and a coefficient the penalty zeroes carries no p-value.
- **Decided:** separation is refused (`PROCESSING_REGRESSION_SEPARATION`), never returned with a warning; the jackknife is weighted (JK1) while bootstrap + weight stays refused; leave-one-out deletes fitted rows only.
- **Decided:** target encoding takes a `fit_on` expression (+ optional `folds`) and ignores weights; only `fit_on` silences the leakage warning.

## Handed on

| Item | To | Why |
|---|---|---|
| #479 `WIN_FORMULA` (a general post-window formula site) | v1.x backlog (no unit) | `WIN_LIFT` covers the gains table in v1; the general site is additive |
| Credit showcase conversion (README requests 11–14, REPRODUCE.md) | E3-S5 (pulse-casebook) | [pulse-casebook#6](https://github.com/frankbardon/pulse-casebook/pull/6), merge after this PR |
