---
id: U54
slug: multi-grouper
title: "Two or more groupers give one row per key combination, in every execution path"
track: API & release
size: M
status: not-started
depends_on: []
soft_depends_on: [U27]
blocks: [U32, U46]
todo_items: [200, 318, 421, 422]
branch: multi-grouper
---

# U54 — multi-grouper

**Outcome:** Two or more groupers give one row per key combination, in every execution path.

**Track:** API & release · **Size:** M · **Depends on:** none · **Soft:** [U27](U27-vector-metrics-aggregates.md) (lands right after it; shares `grouped_tail.go` / `WriteAggregationResult`) · **Unblocks:** [U46](U46-grouped-analysis.md) (overlay partitioning and grouped tests build on composite keys), [U32](U32-docs-audit.md)

## Summary

A plain `groups` list with two or more entries has only ever run its first grouper. `[{GROUP_CATEGORY region}, {GROUP_CATEGORY sex}]` over 30 rows returns 3 rows keyed by `region` alone, with no `sex` column and no warning or error. Every path reads `req.Groups[0]`. That covers buffered and streaming `Process`, `ProcessStream`, sort, `return` shaping, the parallel-decode and shard reducers, Compose slots, grouped matrices and the `max_groups` estimate. The code has done this since the initial commit ("Use the first group for now (single-level grouping)"). Meanwhile `skills/grouper-design.md` ("Composition (key product)") promises a cartesian key product and calls plain `groups` "the canonical cross-tab mechanism". Agents that follow the skill get silently collapsed results. Predict lists every grouper, so predict and runtime disagree without either saying so. Only `Request.Crosstab` axes compose groupers correctly; they have their own composite-key code.

Found 2026-10-11 during U27 (owner report: "groupers have broken when there is more than one"). It is not a regression: origin/main, the U27 branch and the tags v0.39.0, v1.0.0-alpha.5 and v1.0.0-alpha.9 all behave the same. **Owner decision 2026-10-11: this unit runs next, immediately after U27.** It takes over #200 from [U35](U35-predict-runtime-parity.md) and #318 (the "execute" half of #200) from [U46](U46-grouped-analysis.md).

## References

**Read before starting:**
- `skills/grouper-design.md` (Composition section — the contract this unit makes true), `skills/crosstab-guide.md`
- `.claude/reference/execution-modes.md` (streaming vs buffered grouped paths, parallel and shard reducers, crosstab fusion), `.claude/reference/response-components.md` (grouped Components), `.claude/reference/update-demand.md`
- Code sites that read only `Groups[0]` (line numbers as of the U27 branch): `internal/processing/processor.go:881` (`processStreamingGrouped`), `processor.go:2013` (`processGrouped`), `internal/service/parallel_reduce.go:115`, `internal/service/shard_reduce.go:334/738/871`, `internal/processing/matrix_grouped.go:55`, `internal/descriptor/limits.go:333` (`EstimateGroups`)
- Reuse candidate: crosstab's per-axis composite key (`internal/processing/crosstab_fused.go`, around line 1370)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#200** (1. API surface & release pipeline › Public Go surface › Predict / runtime parity) A request with more than one `Groups` entry executes every group or is refused; today only `Groups[0]` runs
- [ ] **#318** (Grouped analysis) Composite group keys: `groups` with N ≥ 2 forms the cartesian key product in streaming and buffered grouped Process, with grouped Components and predict cardinality; delivers the "execute" half of #200
- [ ] **#421** (Grouped analysis) Every reducer and consumer honours composite keys: parallel decode, shard archive (`shard_reduce.go`), Compose slots, ProcessChain stage typing, `sort`, `return` shaping, windows / post-tests partitioning, grouped matrices (`matrix_grouped.go`) and `EstimateGroups` / `max_groups` (product of all groupers' cardinalities)
- [ ] **#422** (Grouped analysis) Regression gate: a table-driven test with 2 and 3 groupers (category × category, range × category, date × category; empty-combination and null-key cases) checked against hand-computed counts in every execution mode, plus a predict/runtime parity row; `grouper-design.md` and the `op-group-*` skills describe exactly what runs

## Scope

**In scope**
- Composite keys across every grouped execution path, with each Data row carrying every grouper's key column
- Grouped Components (per-grouper floor plus composite-row `Groups`) and predict cardinality
- Grouped matrices and the fitted attributes on composite keys, if the per-row key rewrite makes them straightforward; otherwise an explicit refusal and a deferral to U35's #404
- Correcting the skills and docs that over- or under-promise (`grouper-design.md`, `matrix-results.md` "Only the first `groups` entry is executed")

**Out of scope**
- Series-overlay partitioning, grouped tests and multi-aggregation overlay keys ([U46](U46-grouped-analysis.md) #319–#323)
- Banner tables with independent column groups ([U47](U47-survey-tables.md))
- Per-`Groups` multivariate tests (#404, [U35](U35-predict-runtime-parity.md))

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(multi-grouper/E<n>-S<m>): …`; close each epic with `milestone(multi-grouper/E<n>): vertical slice complete — <epic title>`.

### E1 — Two groupers give the right rows
- S1: the regression gate first (#422), failing on today's code; buffered and streaming grouped Process build the composite key from every grouper and emit every key column (#318)
- S2: predict cardinality and `EstimateGroups` use the product of all groupers; grouped Components for composite rows; predict/runtime parity row (#200)

### E2 — Every path agrees
- S1: parallel-decode and shard reducers, Compose slots, ProcessChain typing (#421)
- S2: sort, `return`, windows / post-tests, grouped matrices (execute or refuse explicitly) (#421)
- S3: skills and docs say exactly what runs; `make docs` (#422)

## Acceptance criteria

- [ ] `[region, sex]` over the 30-row repro returns 6 rows, each carrying `region` and `sex`, in buffered, streaming, parallel-decode, shard-archive, sort, Compose and `return`-shaped runs.
- [ ] No grouped path reads `Groups[0]` alone. A grep gate or a structural test enforces this.
- [ ] Predict's group cardinality and the `max_groups` limit agree with the runtime row count.
- [ ] Single-grouper output is byte-identical to before (goldens unchanged).
- [ ] Whatever cannot run on composite keys is refused with a coded error on both predict and runtime. No path silently drops a grouper.

## Gates & tests

- New: the #422 multi-grouper gate (every mode × 2 and 3 groupers), composite-key streaming/buffered parity, shard/parallel bit-exactness on composite keys
- Existing: `TestPredict_Streamable_MatchesRuntime`, the grouped Components suites, `TestObservabilityOffNoAllocs` (the single-grouper path must not gain allocations)

## Update Demand companions

- `skills/grouper-design.md`, the `op-group-*` atomic skills, `skills/matrix-results.md`, `skills/crosstab-guide.md` (when to use `groups` vs `crosstab`)
- `.claude/reference/execution-modes.md` (grouped paths), `.claude/reference/response-components.md` (composite-row Components)
- Payload-schema golden only if the wire shape changes (it should not: every key column is a plain Data field)

## Handed on

| Item | To | Why |
|---|---|---|
| #428 crosstab refits a nested `GROUP_QUANTILE` per outer bucket (plain `groups` fit once) | [U46](U46-grouped-analysis.md) | Owner decision: crosstab behaviour is not changed in U54; the two rules must be reconciled deliberately |
| #429 `OVERLAY_YOY` over two or more groupers (refused in U54) | [U46](U46-grouped-analysis.md) | U46 owns series-overlay partitioning (#320); YoY's per-partition semantics belong with it |

## Human inputs & decisions

- **Decided (owner, 2026-10-11):** run next, after U27. Execute composite keys; a refusal alone is not enough.
- **Open:** order of the composite rows (lexicographic by grouper order, or by each grouper's own key order) and whether empty combinations are emitted. Default proposal: no empty combinations, rows ordered by each grouper's native key order, outer to inner. This matches crosstab's long shape.
