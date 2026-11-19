---
id: U75
slug: zones-labels-2
title: "Cohort default zone, label binding and attribute edge cases land with their parity proofs."
track: Time zones
size: M
status: done
depends_on: [U77, U78]
soft_depends_on: []
blocks: [U32]
todo_items: [480, 481, 482, 483, 484, 485, 714, 716]
branch: zones-labels-2
---

# U75 — zones-labels-2

**Outcome:** Cohort default zone, label binding and attribute edge cases land with their parity proofs.

**Track:** Time zones · **Size:** M · **Depends on:** U77, U78 (U77, U78: CI and test hygiene land first)

## Summary

Time-zone and label depth: a cohort-level default zone, per-row zone runtime equivalence through joins and chains, a zero-spread attribute warning, label bindings on convert and on numeric fields, and a sorted-input fast path for the previous-state attribute.

Shipped in six epics: the cohort default zone (E1), per-row zones through joins (E2), coded signals instead of silence (E3), labels on convert and numeric codes (E4), the sorted-input fast path (E5) and close-out (E6). Every owned item shipped in-unit; one sub-note of #481 is handed on (below).

## Owned TODO items

- [x] **#480** A cohort-level default zone: a schema-declared zone column (a field the schema marks as the row's IANA zone) so every request inherits it with no `time_zone_field` on each slot. Needs a `0x02` schema section or a sidecar, so it is a byte-layout change. Interview decision in U50: v1.x backlog · [U75](units/U75-zones-labels-2.md)
- [x] **#481** Per-row zone runtime equivalence through joins and chain stages ≥1: only the refusal tests exist today (a grouped-cohort `0x02` filter precompute is covered by a unit test only). Found in U50 (E1-S2) · [U75](units/U75-zones-labels-2.md)
- [x] **#482** `ATTR_ZSCORE` / `ATTR_TSCORE` / `ATTR_NORMALIZED` over a field with zero spread score every row null with only the aggregation's `n_null` as a sign; the multivariate attributes warn `PULSE_MATRIX_SINGULAR`. Consider a warning (a new or reused code). Found in U50 (E7-S2) · [U75](units/U75-zones-labels-2.md)
- [x] **#714** A predict advisory for a nullable read fed to arithmetic or a function in an expression (`ATTR_FORMULA` `x * 2` over a nullable `x`): the runtime aborts the run with `PROCESSING_RUNTIME` on the first null row, while predict says nothing. `PULSE_ADVISORY_FORMULA_NULLABLE_RESULT` (#475) covers only a nil reaching the RESULT position, by design. Decide the code (a sibling advisory or a widened one), reuse `exprident`'s guard analysis (`??`, `== nil` / `!= nil` ternaries) and pin fired ⇔ the run aborts on a null row. Found in U67 (E5-S5) · [U75](units/U75-zones-labels-2.md)
- [x] **#716** An `AGG_SET_*` aggregator over an `f64` chain column (stage 1 and later): predict and runtime share the refusal code but differ in message and `details`; pre-existing, found in U66 (E3-S3) · [U75](units/U75-zones-labels-2.md)
- [x] **#483** `Pulse.PredictConvert` facade and a `pulse convert --labels` flag: library `ConvertJob.Predict` without a resolver cannot raise `PULSE_LABEL_KEY_INVALID`, and the CLI cannot bind labels on a convert. Found in U50 (E6-S2) · [U75](units/U75-zones-labels-2.md)
- [x] **#484** Numeric label bindings on process / sample / facet / chain responses: `descriptor.ValidateLabels` still refuses a numeric field (replace mode would turn a number into a string, so it is a design call). Found in U50 (E3-S2) · [U75](units/U75-zones-labels-2.md)
- [x] **#485** `ATTR_PREVIOUS_STATE` sorted-input O(subjects) fast path: a cohort already ordered by subject then instant is scored in one pass with no per-subject sort. Found in U55 (E1-S1) · [U75](units/U75-zones-labels-2.md)

## Close-out notes

- **#481** (E2-S1): per-row zones through a join are proven — the per-row oracle runs every case with the zone column on the left cohort, on the right (`r_tz`), through the left cohort's default, and with null zone cells, against a wrong-zone decoy on the other side; each equals the per-site single-`tz` runs combined and predict echoes the zone field the run read (`TestTimeZoneField_JoinPerRowEqualsSingleZoneSlots`, `TestTimeZoneField_JoinNullZoneCellIsNullLocalValue`, both heavy; `TestTimeZoneField_JoinPerRowEchoMatchesRuntime`). Chain stage ≥ 1 is NOT a gap: no datetime survives a stage, so stage-≥ 1 equivalence is impossible by construction; any `time_zone_field` / `tz_field` there is now one permanent `SERVICE_VALIDATION` refusal, predict ≡ runtime (`TestTimeZoneField_ChainStageZoneFieldRefusedPermanently`). The grouped-cohort `0x02` filter-precompute note in the item is NOT closed (still covered by its unit test only): it is filed as #735 under U33, not ticked here. Ticked at close-out; the unit-test-only sub-note is handed on as #735 (below).

## What shipped

- **E1 — a cohort declares its own default zone (#480).** New REQUIRED `0x02` schema-extension section, tag 3 (ZONE); `encoding.Schema.ZoneField`. Precedence rung `cohort` between request `time_zone` and `Options.DefaultTimeZone`; `InspectResult.ZoneField`. Declared by `ImportJob` / `ConvertJob` `ZoneField` (`--zone-field`, MCP `pulse_import`) or set / cleared on an existing cohort by `Pulse.SetCohortZoneField` / `ClearCohortZoneField` and `pulse cohort zone {set,clear}`. Every rewriter keeps it; shards must agree (`PULSE_SHARD_SCHEMA_MISMATCH` `dimension: zone_field`); an archive rewrite warns `PULSE_SHARD_ZONE_REWRITTEN`; a managed cohort's import sidecar follows (E1-S5).
- **E2 — per-row zones through joins (#481).** Join runtime-equivalence runners (left / right zone column, left cohort default, null zone cells); chain stage >= 1 `time_zone_field` / `tz_field` is one permanent `SERVICE_VALIDATION` refusal.
- **E3 — coded signals (#482, #714, #716).** `PULSE_ATTR_ZERO_SPREAD`; `PULSE_ADVISORY_FORMULA_NULLABLE_OPERAND`; the four set families raise the accepted-types refusal at runtime, byte-identical to predict.
- **E4 — labels (#483, #484).** `Pulse.PredictConvert`, `--labels` on `convert` / `convert predict`; augment labels on `u4`-`u32` for responses, `replace` refused `PULSE_LABEL_REPLACE_NUMERIC`; a labelled integer facet is discrete per-code counts (E4-S3, owner-added).
- **E5 — sorted-input fast path (#485).** `ATTR_PREVIOUS_STATE` runs in one pass on subject-sorted input (benchmark: ~12.7 ms / 164 allocs vs ~30.5 ms / 160k allocs on 200k rows), byte-identical output.

## Outcome / decisions

Owner-delegated design calls Z1–Z14 are in the decisions table of the PR. Implement-time calls (all listed in the PR body):

- **E5-S1** keeps the up-front rows-buffer allocation (sorted 12.7 ms / 8.1 MB vs 14.5 ms / 22.8 MB with append-growth; a 3.2 MB transient on unsorted input).
- **E1-S3** adds no `features.go` row for the cohort default (rides `capability:import` like `--vector` / `--group`); zone_field refusals stay `SERVICE_VALIDATION` on the CLI; convert without keep-pulse is `reason: no_cohort`, a managed passthrough `reason: passthrough`.
- **E1-S4** binds `cohort zone {set,clear}` to `capability:import`; materialize carries the default under the copying column's output name; `NewCohortBuilder` now honours `Schema.ZoneField`; set / clear carries the SPSS sidecar and reports the point-lookup index stale.
- **E1-S5** syncs the managed sidecar `zone_field` after a successful rewrite (runs even when the cohort is unchanged); a sidecar that cannot be updated is a `SERVICE_RESOURCE` `sidecar_warnings` entry.
- **E2-S1** refuses stage >= 1 `time_zone_field` / `tz_field` before the zone pass, one message (code change for a slot `tz_field`: `PROCESSING_CONFIG` -> `SERVICE_VALIDATION`).
- **E3-S1** an all-null / empty base does not warn; Strict now also fails on the streaming two-pass arm.
- **E3-S2** `operand_kind` is a list aligned with `fields`; an extra `index` kind; predict now calls an extension attribute's Factory (for labels a formula reads); flagged for owner review.
- **E4-S1** `convert` uses the facade only with `--labels`; `ConvertJob.Predict` now refuses an unknown `Includes` field.
- **E4-S3** (owner-added) a lookup miss on a labelled integer facet keeps the raw code in the `<field>_label` sibling with one `PULSE_LABEL_LOOKUP_MISS`; KS_VS_POP refused `REF_INCOMPATIBLE_WITH_SHAPE` `label_coded`; the FACET-host arm gate is one shared predict / runtime rule (beyond-story behaviour change); flagged for owner sign-off.

## Handed-on

| Item | To | Why |
|---|---|---|
| #735 black-box proof that the grouped-cohort `0x02` filter precompute reads a per-row zone | [U33](U33-v1-release.md) | #481's sub-note: still covered by a unit test only; the join / chain equivalence the item named is proven |
