---
id: U42
slug: fit-scoring
title: "A fitted model is applied to rows by name, its error is an aggregator, and its interval bounds pass through"
track: API & release
size: L
status: done
depends_on: [U41]
soft_depends_on: []
blocks: [U43]
todo_items: [286, 287, 288, 289, 549]
branch: fit-scoring
---

# U42 — fit-scoring

**Outcome:** A fitted model is applied to rows by name, its error is an aggregator, and its interval bounds pass through.

**Track:** API & release · **Size:** L · **Depends on:** [U41](U41-derived-cohort.md) · **Unblocks:** [U43](U43-post-aggregation-ratios.md)

## Summary

The worked example fitted a regression, then typed the coefficients, rounded to four decimals, into later formulas to score a holdout, compute contributions and build scenario slots. The rounding means the results are only approximately the fit; a one-hot ternary chain restated the categorical predictor by hand; the holdout error was a hand-expanded formula; and the interval bounds (coefficient plus or minus 1.96 standard errors) were computed outside and pasted in.

This unit adds a scoring attribute that applies a named fit to rows (`ATTR_REG_PREDICT`), with the model reused across requests rather than copied. It adds error aggregators (mean absolute percentage error, root mean squared error) over a target and a prediction, and passes the fit's interval bounds through to the contribution calculation so nothing is retyped. Scenario slots then name the fit instead of restating it, which closes the part of the scenario-slot gap that U40's sweep does not.

**Source of inspiration:** the maintainer's marketing-mix worked example. Its inputs, requests and verified outputs are checked in at [`fixtures/mmm-harbor-pine/`](../fixtures/mmm-harbor-pine/README.md). The sibling units are [U40](U40-compose-sweep.md) (sweep and rank), [U41](U41-derived-cohort.md), [U42](U42-fit-scoring.md) and [U43](U43-post-aggregation-ratios.md), in that order; together they make the whole example native.

Numbering note: appended after U40, not renumbered. This unit was not in the original plan.

## References

**Read before starting:**
- CLAUDE.md "Update Demand", "Output Format Contract" and "Byte-layout invariants"
- `.claude/reference/update-demand.md`: add the per-slot rows this unit needs
- `.claude/reference/execution-modes.md`: process, streaming and chain wiring
- `.claude/reference/architecture.md`: adding a public symbol, `TestPublicAPIGolden`
- `.claude/reference/weighting.md`: every new operator is weight-aware or refuses a weight
- `.claude/reference/guided-analysis.md`: `Purpose` / `Interpretation` for each new operator
- the regression atomic skills (`skills/op-reg-*.md`)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#286** (Fit scoring) `ATTR_REG_PREDICT`: apply a named fit to rows, in-request or from a stored model, with categorical predictors expanded by the engine; no coefficient is ever retyped. Needs a persisted or embedded model reuse path across requests
- [x] **#287** (Fit scoring) Error aggregators over a target and a prediction (`AGG_MAPE`, `AGG_RMSE`), weight-aware, with a train/holdout split by index in one slot
- [x] **#288** (Fit scoring) Interval bounds pass-through: contribution and scenario calculations read the fit's `ci_lower` / `ci_upper` instead of pasted constants
- [x] **#289** (Fit scoring) Acceptance: the example's holdout MAPE, contribution table with intervals and scenario slots run without any copied coefficient

Also delivered: U49's **#345** (GLM scoring on the response scale, and fitting on a subset while scoring every row), by E1-S2. It is ticked in [`TODO.md`](../TODO.md) and recorded in [U49](U49-model-workbench.md).

## Scope

- **Order of work.** The model reuse path (#286) is the load-bearing piece and decides the surface; #287 and #288 build on it.
- **Exactness.** Scoring must use the fit's full-precision coefficients, so a scored value matches the regression's own fitted value bit-for-bit.

**In scope**
- A scoring attribute that applies a named in-request or stored fit
- MAPE and RMSE aggregators with atomic skills, manifest rows, `Purpose` and example tags
- Interval pass-through for contributions and scenarios

**Out of scope**
- New model families
- Prediction intervals for new rows (only the coefficient intervals the example needs)
- Ratios across aggregators ([U43](U43-post-aggregation-ratios.md))

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(fit-scoring/E<n>-S<m>): …`; close each epic with `milestone(fit-scoring/E<n>): vertical slice complete — <epic title>`.

As delivered:

### E1 — A holdout is scored from a shared fit
- S1: `ComposedRequest.fits`: named fits run once before any slot (sweep slots included) and bind into every slot; `ComposedResponse.fits` carries each full-precision result; `PredictCompose` lists them; `PULSE_FIT_INVALID`, `PULSE_MODEL_NAME_COLLISION`, `PULSE_MODEL_UNKNOWN`
- S2: `ATTR_REG_PREDICT {fit, terms, scale}` for OLS (penalties and selection included), GLM and Bayes linear: one scoring kernel with `ATTR_REG_FITTED`, so a scored training row equals its fitted value bit for bit; null on a null input; row-local (streams, chains, materialises). Delivers #345
- S3: `ATTR_REG_FITTED` / `RESIDUAL` / `LEVERAGE` emit null, not 0, on a null input (migration guide row)

### E2 — Model error and intervals are native
- S1: `AGG_MAPE`, `AGG_RMSE`, `AGG_MAE` over `field` (actual) and `params.prediction_field`: weight-aware, mergeable, oracle-tested; operator keys `n_scored`, `n_null_prediction`, `n_zero_actual`
- S2: every regression reports `ci_lower` / `ci_upper` / `df` / `conf_level` (default 0.95): t on the residual df for OLS (weighted N*), Wald for GLM, posterior for Bayes, z under resampling
- S3: `ATTR_REG_PREDICT` `bound: lower|upper` on a one-term score, and `coef(fit, term[, stat])` in `ATTR_FORMULA` and `FILTER_EXPRESSION`

### E3 — A fit outlives the request
- S1: `save` on a fit entry or any regression spec writes a versioned `.pulse-model.json` (atomic, full precision, overwrite warns); table directories skip model files
- S2: `Request.models: [{name, path}]` loads a saved model anywhere a request runs, in the one name namespace; predict reads the file and refuses a bad one before anything runs

### E4 — The MMM example is native
- S1: the fixture's steps 09–11 run with no copied coefficient or pasted bound (08 saves the model, 10 loads it, 11 is a zip sweep with `sweep.overlays`); roadmap closed out

## Acceptance criteria

- [x] A scored value equals the regression's fitted value bit-for-bit (unit tests, and all 728 training rows of the fixture)
- [x] The example's holdout error and interval bounds are produced by Pulse with no pasted constant. See the fixture README, "The fit-scoring acceptance"
- [x] Every new operator has an atomic skill, manifest entry, `Purpose`, example tag and a weight class
- [x] Re-run the MMM fixture by hand and convert the steps this unit covers to native Pulse; paste the results into the PR body
- [x] The fixture README's "still outside Pulse" list drops every gap this unit closes (7, 8, 9, 12, 13)
- [x] `format_version` stays `"1.1"`; every wire change is additive. `ATTR_REG_FITTED` / `RESIDUAL` / `LEVERAGE` now emit null on a null input, listed in the migration guide
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAll*`, `TestManifest*Complete`, `TestEveryOperatorHasAnExampleTag`, `TestFeaturesHaveSince`, `TestProfileDependenciesComplete`, `TestPayloadSchemaGolden`
- New: bit-for-bit fitted-vs-scored test, MAPE/RMSE oracle against a reference, predict/runtime parity

## Update Demand companions

- Atomic skills (`op-attr-reg-predict`, `op-agg-mape`, `op-agg-rmse`), the matching `internal/descriptor/capabilities_*.go`, an example `_meta.operators` tag, and `make docs`
- `Purpose` / `Interpretation` entries; `features.go` rows with `Since` and dependency edges
- `weighting.md` and the operator's weight class; payload-schema golden; `response-components.md` for the operator `ComponentSchema`

## Human inputs & decisions

- **Decided (2026-10-10):** ships in v1.0.0. The owner wants Pulse as self-sufficient as possible at v1; this unit is in U32's `depends_on`.
- **Decided (2026-10-10):** a reusable model lives in the request first: a compose `fits` entry fits once for every slot. It can also be saved to a standalone, versioned model file (`save`) and loaded by name in any request (`models`); a save without a load would be a write-only file. Fits and loaded models share one name namespace.
- **Decided (2026-10-10):** the reference level is not expressed at all. A model stores coefficients keyed by predictor column name and its predictor list is authoritative: the scoring slot builds the same columns, a one-hot column absent from `predictors` is the reference level, and scoring never infers a level.
- **Decided (2026-10-10):** no new split mechanism. A train/holdout split uses the existing filters or a `GROUP_RANGE` grouper; a fit request's own filter picks its training rows.
- **Decided (2026-10-10):** intervals are t on the residual df for OLS (weighted: N*), Wald for GLM and posterior for Bayes, at `conf_level` 0.95 by default, so the fixture's pasted 1.96·SE bounds move at about the 3rd significant digit.
- **Decided (2026-10-10, owner):** an error aggregator's universal `n_null` counts nulls in `field` (the actual) only; nulls in the prediction are `n_null_prediction`.

## Handed on

Everything found during this unit and not delivered by it. Each row has a TODO id and an owner; `#N` are TODO ids in [`TODO.md`](../TODO.md), not GitHub issues.

| Found in | What is still open | TODO | Owner |
|---|---|---|---|
| E2-S1 | Predict rebuilds the aggregator capability table on every call (`aggregatorComponentSchemaIndex` → `aggregatorCapabilities` → `withWeightAware`); the off-path allocation pin grew from 236 to 251. Build it once | #390 | [U51](U51-archive-scale.md) |
| E1-S2 | `internal/mergegate`'s built-in row-local list omits `ATTR_CODE_IN` and the `ATTR_SET_*` attributes (pre-existing) | #391 | [U35](U35-predict-runtime-parity.md) |
| E1-S1 | `pulse api compose --stream` emits no `fits` record; an optional terminal `{"fits":…}` NDJSON line, like the sweep's `ranking` line | #392 | U40 follow-up |
| E3-S2 | `FilterToFileWithRequest` and `SampleWithRequest` do not load `Request.models`; a `coef()` there is refused as `PULSE_MODEL_UNKNOWN` | #393 | [U35](U35-predict-runtime-parity.md) |
| E2-S3 | The parallel-decode path (`parallel_reduce.go` → `BuildFiltersWithModels`) has no direct test (it needs an mmap'd OS file); the shard path is tested | #394 | [U35](U35-predict-runtime-parity.md) |
| E2-S1 | No grouper takes explicit numeric ranges, so a train/holdout split that is not a multiple of the `GROUP_RANGE` interval needs a filter or two slots | #395 | [U46](U46-grouped-analysis.md) |
| E1-S1 | `tool-compose.md` is over its soft budget and does not mention `fits` or `models` | #396 | [U38](U38-skill-sync.md) |
| E4-S1 | A plain (non-sweep) compose slot silently ignores an unknown grouper key (`"label"` on `GROUP_RANGE`) while a sweep body's strict decode refuses it; refuse it the same way or unify the paths | #397 | [U44](U44-strict-input.md) |
