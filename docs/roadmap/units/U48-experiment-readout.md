---
id: U48
slug: experiment-readout
title: "An experiment readout is one request: sample-ratio check, a named reference arm, an adjusted effect with its interval, all in one correction family"
track: Statistical integrity
size: M
status: done
depends_on: []
soft_depends_on: [U43, U46]
blocks: [U32]
todo_items: [332, 333, 334, 335, 336, 337, 338]
branch: experiment-readout
---

# U48 — experiment-readout

**Outcome:** An experiment readout is one request: sample-ratio check, a named reference arm, an adjusted effect with its interval, all in one correction family.

**Track:** Statistical integrity · **Size:** M · **Depends on:** none · **Soft:** [U43](U43-post-aggregation-ratios.md), [U46](U46-grouped-analysis.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

The A/B readout ran every statistic in Pulse, but through detours. There is no goodness-of-fit test, so the sample-ratio-mismatch check used a two-row design cohort and a facet overlay. Two-group tests report `diff` as alphabetical first minus second, so control − variant, and every lift had to be sign-flipped by the reader. `REG_OLS` gives no coefficient interval and its p-values cannot join a `multiplicity` family. Covariate adjustment (CUPED) took two round trips, with the slope pasted into a formula. Predict reports a tests-only request as non-streamable with a 19 MB size estimate, and `predict-compose` shows no per-slot p-value count, so the cross-slot correction family is invisible before the run.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([A/B test readout](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/ab-test-readout/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `skills/statistical-testing.md`, `skills/multiplicity-correction.md`, `skills/regression-modeling.md`, the `op-test-*` and `op-reg-ols` skills
- `.claude/reference/execution-modes.md` (Multiplicity) and `.claude/reference/predict-inspect.md`
- [U43](U43-post-aggregation-ratios.md) (cohort-total scalars), [U46](U46-grouped-analysis.md) (grouped tests)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#332** (Experiment readout) Goodness-of-fit chi-square against supplied expected proportions (`TEST_CHISQ` `params.expected` or a dedicated GOF test) and a one-sample `TEST_PROP_Z` (`p0`), so a sample-ratio check is one test
- [x] **#333** (Experiment readout) `params.reference` on two-group tests (`TEST_PROP_Z`, `TEST_WELCH`, `TEST_T`, `TEST_MANN_WHITNEY_U`, `TEST_Z_TWO_SAMPLE`) so `diff` and its interval read treatment − reference
- [x] **#334** (Experiment readout) Regression results carry coefficient confidence intervals at the request alpha, and regression p-values can join a `multiplicity` family
- [x] **#335** (Experiment readout) Covariate-adjusted comparisons in one call: a `covariate` on the two-group mean and proportion tests (CUPED), with the adjusted estimate, interval and variance reduction reported
- [x] **#336** (Experiment readout) Predict accuracy for test-only and regression-only requests (streamability and size), and `predict-compose` reports per-slot streamability, p-value counts and the cross-slot multiplicity advisory
- [x] **#337** (Experiment readout) Polish: `TEST_CHISQ` labels ordered consistently with the other tests; the Mann-Whitney ties warning says the test mostly compares the share of tied values
- [x] **#338** (Experiment readout) Acceptance: the A/B readout drops the design cohort and facet SRM workaround, the two-step CUPED and every sign flip

## Scope

**In scope**
- GOF and one-sample tests
- Reference arm
- Regression intervals and multiplicity
- CUPED
- Predict accuracy for test batteries

**Out of scope**
- Sequential testing and always-valid p-values (stated as a limit in `skills/statistical-testing.md`, `op-test-welch`, `op-test-prop-z`)
- Bayesian experiment analysis (stated as a limit in the same skills)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(experiment-readout/E<n>-S<m>): …`; close each epic with `milestone(experiment-readout/E<n>): vertical slice complete — <epic title>`.

### E1 — The checks and the sign
- S1: GOF chi-square and one-sample proportion
- S2: `params.reference` across two-group tests

### E2 — Adjusted effects
- S1: regression intervals and multiplicity membership
- S2: covariate-adjusted tests

### E3 — Predict tells the truth
- S1: test-only streamability and size; predict-compose per-slot detail
- S2: polish; convert the A/B example

## Acceptance criteria

- [x] The SRM χ² from the GOF test equals the facet-overlay result (0.76, p 0.38 on the showcase data) — `TestExperimentSRM_GOFEqualsChiSqVsPopFacet`, `TestExperimentReadout_EndToEnd`
- [x] The CUPED-adjusted interval equals the two-step result — `TestExperimentCUPED_OracleEqualsTwoStep`
- [ ] The A/B README quotes every lift without a sign flip (casebook PR, E3-S4)
- [ ] Showcase examples simplified where this unit allows (A/B test readout): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse (casebook PR, E3-S4)
- [x] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump) — the behaviour changes are listed in the [migration guide](../v1.0.0-api-and-release/03-embedder-migration.md) "Changes from U48"
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- the reference oracles for each new test, `TestMultiplicityNoneIsIdentity`, `TestPredict_Streamable_MatchesRuntime`, `TestSkillsCoverAllPurposes`, `TestInterpretationCoversOutputs`
- New: GOF and one-sample oracles, reference-arm sign tests, CUPED equivalence, regression-CI oracle
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- Multiplicity row in `update-demand.md` (load first), `skills/multiplicity-correction.md`, the p-emitting atomic skills' `## Params`
- `guided-analysis.md`: `Purpose` / `Interpretation` for any new test; `features.go` rows; `make docs`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Decided:** CUPED is a test parameter (`params.covariate` on `TEST_T` two-sample, `TEST_WELCH`, `TEST_Z_TWO_SAMPLE` and two-proportion `TEST_PROP_Z`): one call, one pass from per-group moments, reporting θ and the variance reduction. The `total()` two-step stays the fallback for a test that refuses a covariate.
- **Decided:** the goodness-of-fit test is `TEST_CHISQ` `params.expected` (the `TEST_T` `mu` precedent), not a new operator; `reference` is per test, no request-level default; tests-only and regression-only requests stream (the runtime route, not only predict).

## Handed on

| Item | To | Why |
|---|---|---|
| #478 skill `tags=[…]` example pointers that no example carries | [U32](U32-docs-audit.md) | docs audit; `statistical-testing`'s own pointers fixed here |
