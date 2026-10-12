---
id: U46
slug: grouped-analysis
title: "Grouped analysis is one request: composite keys, overlays that pick their measure and partition, and tests per group"
track: API & release
size: L
status: not-started
depends_on: [U54]
soft_depends_on: [U35]
blocks: [U32]
todo_items: [319, 320, 321, 322, 323, 395, 428, 429]
branch: grouped-analysis
---

# U46 — grouped-analysis

**Outcome:** Grouped analysis is one request: composite keys, overlays that pick their measure and partition, and tests per group.

**Track:** API & release · **Size:** L · **Depends on:** none · **Soft:** [U35](U35-predict-runtime-parity.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

Every example that asked a per-segment question paid for it in Compose slots. `groups` with two or more groupers silently uses only the first (the multi-time-zone builder found `[warehouse, service_level]` returning 6 rows instead of 12, with no warning), although `grouper-design.md` documents the key product; [U54](U54-multi-grouper.md) (#200, #318) makes it execute. Series overlays always score `aggregations[0]` and cannot be partitioned, so the rolling anomaly screen needed one slot per warehouse and the retail share-of-total needed its aggregations reordered. A Compose reference overlay over slots with two or more aggregations fails with `PULSE_OVERLAY_KEY_SET_DIVERGENT` because other aggregations' values are folded into the key, and `predict-compose` does not catch it. `tests[]` ignore `groups`, so the A/B segment readout took 12 slots.

Composite keys (#200 / #318) moved to [U54](U54-multi-grouper.md) (owner decision 2026-10-11), which lands first. This unit lets series overlays name their measure and partition by leading group keys, makes tests honour groups, and fixes the multi-aggregation overlay key.

**Source of inspiration:** the showcase examples under `docs/showcase/` ([multi-time-zone ops](../../showcase/multi-timezone-ops/README.md), [A/B test readout](../../showcase/ab-test-readout/README.md), [privacy-safe sharing](../../showcase/privacy-safe-sharing/README.md), [retail at scale](../../showcase/retail-at-scale/README.md), [brand tracker](../../showcase/brand-tracker/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `skills/grouper-design.md`, `skills/overlay-system.md`, `skills/statistical-testing.md`, `skills/compose-requests.md`
- `.claude/reference/execution-modes.md` (streaming vs buffered grouped paths, overlays) and `.claude/reference/response-components.md` (grouped Components)
- [U54](U54-multi-grouper.md) (composite keys — must have landed)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#319** (Grouped analysis) Series overlays take a `field` naming the aggregation they score (today always `aggregations[0]`, undocumented), and a Compose series host with several aggregations keys on group columns only; `predict-compose` refuses what the runtime would
- [ ] **#320** (Grouped analysis) `partition_by` on series overlays (rolling z-score and index, delta and index vs prior, share of total) over the leading group keys, mirroring windows
- [ ] **#321** (Grouped analysis) Grouped tests: `tests[]` run once per group when `groups` is set (or via an explicit `within`), with every group's p-value in one multiplicity family; until then predict warns when tests and groups coexist
- [ ] **#322** (Grouped analysis) The series overlay payload matches its skill (`entries[i].value` present, or the skill corrected)
- [ ] **#323** (Grouped analysis) Acceptance: the multi-time-zone warehouse × local-day screen, the A/B segment readout (12 slots), the privacy real-vs-twin comparison (14 slots) and the brand tracker's per-KPI wave deltas each become one request
- [ ] **#428** (Grouped analysis) Crosstab refits a nested data-dependent grouper per outer bucket: `Processor.PartitionByAxis` calls `GROUP_QUANTILE`'s `Group` on each outer bucket's records (only `GROUP_KMEANS` is fitted once over the whole set via `fitWholeSetAxes`), so a crosstab axis `[region, quartile]` bins quartiles within each region while plain `groups` (U54) fit them once over the filtered set. Decide one rule (or expose both, e.g. a `within` knob) and align crosstab, fused crosstab and plain groups. Found in U54 (E1-S1, confirmed E2-S2)
- [ ] **#429** (Grouped analysis) `OVERLAY_YOY` over two or more `groups`: the series host is the composite rows, not one date axis, so YoY is refused (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, `details.groupers`) on predict and runtime. Give it per-partition semantics (prior-year lookup on the `GROUP_DATE` position within each combination of the other keys, sharing #320's `partition_by`) and lift the refusal. Deferred from U54 (E2-S2)

## Scope

**In scope**
- Overlay measure selection, partitioning and multi-aggregation keys
- Tests per group

**Out of scope**
- Banner tables with independent column groups ([U47](U47-survey-tables.md))
- Per-row time zones ([U50](U50-local-time.md))

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(grouped-analysis/E<n>-S<m>): …`; close each epic with `milestone(grouped-analysis/E<n>): vertical slice complete — <epic title>`.

### E1 — Composite keys
Moved to [U54](U54-multi-grouper.md) (owner decision 2026-10-11).

### E2 — Overlays pick and partition
- S1: overlay `field`; multi-aggregation series keys; predict-compose parity
- S2: `partition_by` on series overlays

### E3 — Tests per group
- S1: grouped test execution and multiplicity family
- S2: convert the four showcase requests

## Acceptance criteria

- [ ] `groups: [warehouse, service_level]` returns 12 keyed rows, streaming and buffered, matching a crosstab of the same fields
- [ ] A rolling z-score partitioned by warehouse equals the six single-warehouse runs
- [ ] Segment tests in one request equal the per-segment slots, with one correction family
- [ ] Showcase examples simplified where this unit allows (multi-time-zone ops; A/B segment readout; privacy-safe comparison; brand tracker wave deltas; retail share-of-total): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse
- [ ] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestPredict_Streamable_MatchesRuntime`, `TestComposedResponse_OverlayFreeByteIdentical`, `TestPayloadSchemaGolden`, `TestSkillsCoverAllOverlayKinds`
- New: composite-key streaming/buffered parity, partitioned-overlay equivalence, grouped-test equivalence
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- `skills/grouper-design.md`, `skills/overlay-system.md`, the touched `op-overlay-*` and `op-test-*` skills, `skills/statistical-testing.md`
- `update-demand.md` rows for the new overlay and test slots; payload-schema golden; `response-components.md` if grouped Components move
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Open:** whether grouped tests are implied by `groups` or opt-in via `within`. Implicit is natural but changes today's (ignored) behaviour for existing requests.

## Inherited from U42

Filed from [U42 handed on](U42-fit-scoring.md#handed-on); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#395** — A grouper over explicit numeric ranges (e.g. 0–90 and 91–103), so a train/holdout split that is not a multiple of the `GROUP_RANGE` interval is one slot instead of a filter or two slots. Found in U42 (E2-S1).
