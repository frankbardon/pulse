---
id: U42
slug: fit-scoring
title: "A fitted model is applied to rows by name, its error is an aggregator, and its interval bounds pass through"
track: API & release
size: L
status: not-started
depends_on: [U41]
soft_depends_on: []
blocks: [U43]
todo_items: [286, 287, 288, 289]
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

- [ ] **#286** (Fit scoring) `ATTR_REG_PREDICT`: apply a named fit to rows, in-request or from a stored model, with categorical predictors expanded by the engine; no coefficient is ever retyped. Needs a persisted or embedded model reuse path across requests
- [ ] **#287** (Fit scoring) Error aggregators over a target and a prediction (`AGG_MAPE`, `AGG_RMSE`), weight-aware, with a train/holdout split by index in one slot
- [ ] **#288** (Fit scoring) Interval bounds pass-through: contribution and scenario calculations read the fit's `ci_lower` / `ci_upper` instead of pasted constants
- [ ] **#289** (Fit scoring) Acceptance: the example's holdout MAPE, contribution table with intervals and scenario slots run without any copied coefficient

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

### E1 — A fit can be reused
- S1: the model reuse path (embedded in a compose, or stored beside the cohort) and its coded refusals
- S2: `ATTR_REG_PREDICT`, with atomic skill, manifest and predict parity

### E2 — Error and intervals are native
- S1: `AGG_MAPE` / `AGG_RMSE`, weight-aware, with the holdout split
- S2: interval pass-through; the fixture's holdout, contribution and scenario steps converted

## Acceptance criteria

- [ ] A scored value equals the regression's fitted value bit-for-bit
- [ ] The example's holdout error and interval bounds are produced by Pulse with no pasted constant
- [ ] Every new operator has an atomic skill, manifest entry, `Purpose`, example tag and a weight class
- [ ] Re-run the MMM fixture by hand and convert the steps this unit covers to native Pulse; paste the results into the PR body
- [ ] The fixture README's "still outside Pulse" list drops every gap this unit closes
- [ ] `format_version` stays `"1.1"`; every change is additive
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAll*`, `TestManifest*Complete`, `TestEveryOperatorHasAnExampleTag`, `TestFeaturesHaveSince`, `TestProfileDependenciesComplete`, `TestPayloadSchemaGolden`
- New: bit-for-bit fitted-vs-scored test, MAPE/RMSE oracle against a reference, predict/runtime parity

## Update Demand companions

- Atomic skills (`op-attr-reg-predict`, `op-agg-mape`, `op-agg-rmse`), the matching `internal/descriptor/capabilities_*.go`, an example `_meta.operators` tag, and `make docs`
- `Purpose` / `Interpretation` entries; `features.go` rows with `Since` and dependency edges
- `weighting.md` and the operator's weight class; payload-schema golden; `response-components.md` for the operator `ComponentSchema`

## Human inputs & decisions

- **Open:** v1 membership: owner call. This unit is written as a post-U40 unit; the owner decides whether it ships in v1.0.0 (then add it to U32's `depends_on`) or after.
- **Open:** where a reusable model lives: inside one `ComposedRequest`, a stored artefact beside the cohort, or both.
- **Open:** how a categorical predictor's reference level is expressed when scoring.
- **Open:** gap 8's train/holdout split by index: a filter, a grouper or a slot param.
