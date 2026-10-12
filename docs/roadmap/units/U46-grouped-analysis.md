---
id: U46
slug: grouped-analysis
title: "Grouped analysis is one request: composite keys, overlays that pick their measure and partition, and tests per group"
track: API & release
size: L
status: done
depends_on: [U54]
soft_depends_on: [U35]
blocks: [U32]
todo_items: [319, 320, 321, 322, 323, 428, 429, 430]
branch: grouped-analysis
---

# U46 — grouped-analysis

**Outcome:** Grouped analysis is one request: composite keys, overlays that pick their measure and partition, and tests per group.

**Track:** API & release · **Size:** L · **Depends on:** none · **Soft:** [U35](U35-predict-runtime-parity.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

Every example that asked a per-segment question paid for it in Compose slots. `groups` with two or more groupers silently used only the first (the multi-time-zone builder found `[warehouse, service_level]` returning 6 rows instead of 12, with no warning), although `grouper-design.md` documents the key product; [U54](U54-multi-grouper.md) (#200, #318) made it execute (landed). Series overlays (`OVERLAY_DELTA_VS_PRIOR` and kin) still run down the composite rows and cross outer-bucket boundaries; #320 partitions them. Series overlays always score `aggregations[0]` and cannot be partitioned, so the rolling anomaly screen needed one slot per warehouse and the retail share-of-total needed its aggregations reordered. A Compose reference overlay over slots with two or more aggregations fails with `PULSE_OVERLAY_KEY_SET_DIVERGENT` because other aggregations' values are folded into the key, and `predict-compose` does not catch it. `tests[]` ignore `groups`, so the A/B segment readout took 12 slots.

Composite keys (#200 / #318) moved to [U54](U54-multi-grouper.md) (owner decision 2026-10-11), which lands first. This unit lets series overlays name their measure and partition by leading group keys, makes tests honour groups, and fixes the multi-aggregation overlay key.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([multi-time-zone ops](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/multi-timezone-ops/README.md), [A/B test readout](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/ab-test-readout/README.md), [privacy-safe sharing](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/privacy-safe-sharing/README.md), [retail at scale](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/retail-at-scale/README.md), [brand tracker](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/brand-tracker/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `skills/grouper-design.md`, `skills/overlay-system.md`, `skills/statistical-testing.md`, `skills/compose-requests.md`
- `.claude/reference/execution-modes.md` (streaming vs buffered grouped paths, overlays) and `.claude/reference/response-components.md` (grouped Components)
- [U54](U54-multi-grouper.md) (composite keys — must have landed)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#319** (Grouped analysis) Series overlays take a `field` naming the aggregation they score (today always `aggregations[0]`, undocumented), and a Compose series host with several aggregations keys on group columns only; `predict-compose` refuses what the runtime would
- [x] **#320** (Grouped analysis) `partition_by` on series overlays (rolling z-score and index, delta and index vs prior, share of total) over the leading group keys, mirroring windows. Interim (U54 follow-up): over two or more `groups` the prior, rolling, baseline and sibling kinds are refused `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` (`details.groupers`) on predict and runtime, as `OVERLAY_YOY` is (#429); #320 lifts that refusal. Share of total is order-independent (each row over the grand total of every composite row) and is not refused — decide whether a partitioned share (each row over its outer bucket's total) is still wanted here.
- [x] **#321** (Grouped analysis) Grouped tests: `tests[]` run once per group when `groups` is set (or via an explicit `within`), with every group's p-value in one multiplicity family; until then predict warns when tests and groups coexist
- [x] **#322** (Grouped analysis) The series overlay payload matches its skill (`entries[i].value` present, or the skill corrected)
- [x] **#323** (Grouped analysis) Acceptance (rewritten as agreed): the brand tracker's five per-KPI wave-delta slots become one request; the A/B segment readout's 12 slots become four grouped requests (one per segment variable) in one correction family; the multi-time-zone warehouse × day screen is one partitioned request (a request has one `time_zone`, so the per-warehouse local-day boundary waits on [U50](U50-local-time.md)); the retail share-of-total needs no aggregation reorder. The privacy real-vs-twin comparison is NOT delivered here: `groups` cannot span two cohorts (filed as #433)
- [x] **#428** (Grouped analysis) Crosstab refits a nested data-dependent grouper per outer bucket: `Processor.PartitionByAxis` calls `GROUP_QUANTILE`'s `Group` on each outer bucket's records (only `GROUP_KMEANS` is fitted once over the whole set via `fitWholeSetAxes`), so a crosstab axis `[region, quartile]` bins quartiles within each region while plain `groups` (U54) fit them once over the filtered set. Decide one rule (or expose both, e.g. a `within` knob) and align crosstab, fused crosstab and plain groups. Found in U54 (E1-S1, confirmed E2-S2)
- [x] **#429** (Grouped analysis) `OVERLAY_YOY` over two or more `groups`: the series host is the composite rows, not one date axis, so YoY is refused (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `details.groupers`) on predict and runtime. Give it per-partition semantics (prior-year lookup on the `GROUP_DATE` position within each combination of the other keys, sharing #320's `partition_by`) and lift the refusal. Deferred from U54 (E2-S2)
- [x] **#430** (Grouped analysis) Chain stage typing names a grouped stage's key column `g.Field`, not `vectors.GroupKeyField` (`chainPredictedOutputFields` / `synthChainSchema` in `internal/descriptor/chain.go`, `ChainOutputSchema` in `internal/processing/chain.go`); only `GROUP_KMEANS` is affected and chains refuse it today, so nothing is wrong yet; derive the key column from the shared helper before chains accept it. Found in U54 (E2-S3) · [U46](U46-grouped-analysis.md)

## Scope

**In scope**
- Overlay measure selection, partitioning and multi-aggregation keys
- Tests per group

**Out of scope**
- Banner tables with independent column groups ([U47](U47-survey-tables.md))
- Per-row time zones ([U50](U50-local-time.md))

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(grouped-analysis/E<n>-S<m>): …`; close each epic with `milestone(grouped-analysis/E<n>): vertical slice complete — <epic title>`.

Composite keys moved to [U54](U54-multi-grouper.md) (owner decision 2026-10-11). Delivered as four epics on branch `grouped-analysis` (PR #347):

- **E1 — Overlays pick their measure:** `params.field` on process-host, Compose and whole-chain overlays (`PULSE_OVERLAY_FIELD_UNKNOWN`); Compose and chain keys on group columns only.
- **E2 — Overlays partition:** `partition_by` on series overlays (the U54 refusal lifted), `_TOTAL` kinds partitioned, `OVERLAY_YOY` per non-date-key combination, `PULSE_OVERLAY_PARTITION_INVALID`, advisory `PULSE_ADVISORY_SERIES_AXIS_NOT_DATE` (ordered kinds only, owner decision 2026-10-12).
- **E3 — Tests per group:** implicit grouped `tests[]` (`group_key`, test-major, one multiplicity family, `AppliedMultiplicity.excluded`), partitioned `post_tests`, `PULSE_TEST_PARTITION_INVALID`.
- **E4 — One fit rule, chain key, bookkeeping:** `fit: global|within` (#428), the chain key column (#430), the showcase move, and owner-added fixes: chain-stage `field` (E1-S4), within-refit warnings (E4-S5), joined stage-0 defaults (E4-S6), `SLOT_SHAPE_DIVERGENT` runtime parity (E4-S7), array-valued refusal judged on the scored aggregation only (E4-S8, E4-S10), predict-chain shape mismatch as a warning (E4-S9).

Embedder-facing changes: [03-embedder-migration.md](../v1.0.0-api-and-release/03-embedder-migration.md#changes-from-u46-grouped-analysis).

## Acceptance criteria

- [x] `groups: [warehouse, service_level]` returns 12 keyed rows, streaming and buffered, matching a crosstab of the same fields
- [x] A rolling z-score partitioned by warehouse equals the six single-warehouse runs
- [x] Segment tests in one request equal the per-segment slots, with one correction family
- [x] Showcase examples simplified where this unit allows (multi-time-zone ops; A/B segment readout; brand tracker wave deltas; retail share-of-total): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse. The privacy-safe comparison is not convertible (`groups` cannot span two cohorts, #433). **Owner decision 2026-10-12: showcases live in [github.com/frankbardon/pulse-casebook](https://github.com/frankbardon/pulse-casebook), not this repository** (E4-S4 removed them); the conversions ride casebook PR #2, which merges after pulse #347
- [x] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestPredict_Streamable_MatchesRuntime`, `TestComposedResponse_OverlayFreeByteIdentical`, `TestPayloadSchemaGolden`, `TestSkillsCoverAllOverlayKinds`
- New: composite-key streaming/buffered parity, partitioned-overlay equivalence, grouped-test equivalence
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- `skills/grouper-design.md`, `skills/overlay-system.md`, the touched `op-overlay-*` and `op-test-*` skills, `skills/statistical-testing.md`
- `update-demand.md` rows for the new overlay and test slots; payload-schema golden; `response-components.md` if grouped Components move
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Decided:** grouped tests are implied by `groups` (a behaviour change, recorded in the migration doc); run a whole-set test in its own ungrouped request.
- **Decided (owner, 2026-10-12):** showcase examples live in `pulse-casebook`; `PULSE_ADVISORY_SERIES_AXIS_NOT_DATE` fires for the ordered kinds only; `#395` moves to U49.

## Handed on

| Item | To | Why |
|---|---|---|
| #433 privacy real-vs-twin comparison as one request | unowned | `groups` cannot span two cohorts; the showcase keeps a slot per side |
| #434 per-bucket `Components` under `fit: "within"` | unowned | Components still describe the whole-set fit (E4-S1 / E4-S5 decision); no story requested |
| #435 `op-test-trend` ~30% over its soft body budget | [U32](U32-docs-audit.md) | pre-existing; U32 owns the budget flip (#161) |
| #436 unknown overlay `field` on both stages: two predict errors, one runtime | [U35](U35-predict-runtime-parity.md) | parity |
| #437 `MaxEstimatedMemory` ignores per-group test state | [U35](U35-predict-runtime-parity.md) | limits parity with `MaxGroups` |
| per-row local-day boundary for the multi-time-zone screen (#348, #353) | [U50](U50-local-time.md) | already filed; a request has one `time_zone` |

## Inherited from U42 (moved to U49)

Filed from [U42 handed on](U42-fit-scoring.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- **#395** — Moved to [U49](U49-model-workbench.md) (U46 E4-S3, owner decision): a train/holdout split is a model-workbench need. A grouper over explicit numeric ranges (e.g. 0–90 and 91–103), so a train/holdout split that is not a multiple of the `GROUP_RANGE` interval is one slot instead of a filter or two slots. Found in U42 (E2-S1).
