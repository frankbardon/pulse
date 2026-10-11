---
id: U43
slug: post-aggregation-ratios
title: "A ratio of two aggregates and a scalar derived from an aggregate are native, so ROI and a scenario scale factor need no hand arithmetic"
track: API & release
size: M
status: done
depends_on: [U42]
soft_depends_on: []
blocks: []
todo_items: [290, 291, 292]
branch: post-aggregation-ratios
---

# U43 — post-aggregation-ratios

**Outcome:** A ratio of two aggregates and a scalar derived from an aggregate are native, so ROI and a scenario scale factor need no hand arithmetic.

**Track:** API & release · **Size:** M · **Depends on:** [U42](U42-fit-scoring.md) · **Unblocks:** none

## Summary

Contribution sums are native, but ROI (contribution divided by spend) and marginal ROI were divided by the reader, and the "one million a year, pro rata" scenario factors were computed by hand from channel totals and typed into formulas. A response has no field for a ratio of two aggregates, and a row-level formula cannot see a cohort-wide total.

This unit adds the two missing pieces: a post-aggregation ratio output across aggregators (a slot that divides one aggregate by another, or a post-aggregation formula slot), and aggregate-derived scalars inside row formulas, so a formula can reference a cohort total instead of a pasted constant. Together with [U42](U42-fit-scoring.md) this closes the last hand-computed numbers in the worked example.

**Source of inspiration:** the maintainer's marketing-mix worked example. Its inputs, requests and verified outputs are checked in at [`fixtures/mmm-harbor-pine/`](../fixtures/mmm-harbor-pine/README.md). The sibling units are [U40](U40-compose-sweep.md) (sweep and rank), [U41](U41-derived-cohort.md), [U42](U42-fit-scoring.md) and [U43](U43-post-aggregation-ratios.md), in that order; together they make the whole example native.

Numbering note: appended after U40, not renumbered. This unit was not in the original plan.

## References

**Read before starting:**
- CLAUDE.md "Update Demand", "Output Format Contract" and "Byte-layout invariants"
- `.claude/reference/update-demand.md`: add the per-slot rows this unit needs
- `.claude/reference/execution-modes.md`: process, streaming and chain wiring
- `.claude/reference/architecture.md`: adding a public symbol, `TestPublicAPIGolden`
- `.claude/reference/response-components.md`: any new output slot and its component schema
- `.claude/reference/weighting.md`: weighted totals
- `skills/op-attr-formula.md` and the formula design skill

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#290** (Post-aggregation ratios) A ratio output across aggregators (`AGG_RATIO`, or a post-aggregation formula slot) for ROI and marginal ROI, with defined behaviour for a zero or null denominator (`null`, never 0). Delivered with no new operator: the existing `AGG_RATIO` accepts attribute labels
- [x] **#291** (Post-aggregation ratios) Aggregate-derived scalars inside `ATTR_FORMULA`: a cohort-total reference (a `SUM(field)`-style term) evaluated once before row evaluation, with the two-pass cost visible in predict (streamability). Delivered as `total(expr)`
- [x] **#292** (Post-aggregation ratios) Acceptance: the example's ROI, marginal ROI and scenario scale factors come out of Pulse with no hand-computed number

## Scope

- **Order of work.** #291 first: it changes what a formula can see and decides streamability; #290 reads only finished aggregates.
- **Streamability.** A formula that reads a cohort total needs two passes; predict must say so, and the streaming gate must refuse or buffer, never answer wrongly.

**In scope**
- A ratio across aggregators or a post-aggregation formula slot
- Cohort-total references in `ATTR_FORMULA`, with predict streamability parity

**Out of scope**
- Arbitrary multi-pass pipelines
- Regression-aware contribution operators beyond what [U42](U42-fit-scoring.md) delivers

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(post-aggregation-ratios/E<n>-S<m>): …`; close each epic with `milestone(post-aggregation-ratios/E<n>): vertical slice complete — <epic title>`.

### E1 — Aggregate-derived scalars in formulas
- S1: the cohort-total reference, two-pass evaluation, predict streamability parity
- S2: skill, docs and examples

### E2 — Ratios across aggregators
- S1: the ratio output with null-denominator semantics, components and payload schema
- S2: the fixture's ROI and scenario steps converted

## Acceptance criteria

- [x] A cohort-total reference in a formula matches the hand-computed total exactly (`total(expr)`)
- [x] A ratio of two aggregates is a response field; a zero denominator is `null`, not 0 (the existing `AGG_RATIO`, pinned over derived labels)
- [x] Predict and runtime agree on streamability for the new formula term (`PULSE_ADVISORY_EXTRA_PASS`; sequential fallback on shards and parallel decode)
- [x] Re-run the MMM fixture by hand and convert the steps this unit covers to native Pulse; paste the results into the PR body
- [x] The fixture README's "still outside Pulse" list drops every gap this unit closes (10, 11)
- [x] `format_version` stays `"1.1"`; every change is additive
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestPayloadSchemaGolden`, `TestPredict_Streamable_MatchesRuntime`, `TestSkillsCoverAll*`, `TestEveryOperatorHasAnExampleTag`
- New: ratio null-denominator test, cohort-total formula equivalence test, streamability parity

## Update Demand companions

- Skills: `op-attr-formula.md`, the formula design skill, and the new ratio operator's atomic skill if one is added; `capabilities_*.go`; example tags; `make docs`
- `response-components.md` and CLAUDE.md "Response.Components" only if the component shape moves
- Payload-schema golden; `features.go` rows

## Human inputs & decisions

- **Decided (2026-10-10):** ships in v1.0.0. The owner wants Pulse as self-sufficient as possible at v1; this unit is in U32's `depends_on`.
- **Decided (2026-10-10):** the ratio is the existing `AGG_RATIO`, which already accepts attribute labels; no new operator and no post-aggregation formula slot. It is pinned by tests and the ROI recipe is in its skill.
- **Decided (2026-10-10):** the cohort-total grammar is the function form `total(expr)` with an expression argument, not a reserved name. It reads the post-filter cohort, unweighted, skips nulls, and an empty or zero total is `null`. `total` is reserved against embedder `ExprFunctions`.
- **Decided (2026-10-10):** a `total()` request makes two passes. Shard and parallel-decode runs fall back to the sequential path, `ProcessChain` refuses it (`PULSE_CHAIN_NOT_MERGEABLE`), and predict reports the extra pass as `PULSE_ADVISORY_EXTRA_PASS`. Coded refusals: `PULSE_FORMULA_TOTAL_SITE`, `_NESTED`, `_ARITY`.
- **Decided (2026-10-10):** in the fixture, the four scale factors are derived with `total()` over the model-year base (`week_index >= 52`) and reproduce the old typed factors at 5 decimal places; ROI, bounds and marginal ROI come from `AGG_RATIO`, bit-identical to the reader's division.

## Handed on

Everything found during this unit and not delivered by it. Each row has a TODO id and an owner; `#N` are TODO ids in [`TODO.md`](../TODO.md), not GitHub issues.

| Found in | What is still open | TODO | Owner |
|---|---|---|---|
| E1 | `total()` is unweighted because `ATTR_FORMULA` is not weight-aware; a weight-aware variant | #398 | [U47](U47-survey-tables.md) |
| E1 | Merge `total()` partial sums across partitions so shard and parallel-decode runs stay parallel instead of falling back to sequential | #399 | [U51](U51-archive-scale.md) |
| E2-S2 | Predict-compose runs no per-slot `Request.Overlays` validation: a slot-level `OVERLAY_FORMULA` on a non-crosstab host is refused at runtime (`PULSE_OVERLAY_KIND_UNKNOWN`) but predict-compose is silent (pre-existing) | #400 | [U35](U35-predict-runtime-parity.md) |
