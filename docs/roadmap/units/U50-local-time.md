---
id: U50
slug: local-time
title: "Every row is read in its own local time, and calendar parts sort and filter the way a calendar does"
track: Time zones
size: M
status: done
depends_on: []
soft_depends_on: [U46]
blocks: [U32]
todo_items: [348, 349, 350, 351, 352, 353, 467, 468, 470]
branch: local-time
---

# U50 — local-time

**Outcome:** Every row is read in its own local time, and calendar parts sort and filter the way a calendar does.

**Track:** Time zones · **Size:** M · **Depends on:** none · **Soft:** [U46](U46-grouped-analysis.md) · **Unblocks:** [U32](U32-docs-audit.md) (the docs audit covers it)

## Summary

The multi-time-zone example shows Pulse getting local time right, but only one zone per request: the cohort carries each row's IANA zone, yet `time_zone` / `tz` is per request or slot, so every cross-site local view needed a six-slot Compose and never produced one warehouse × local-hour matrix. Day-of-week keys sort alphabetically and ignore `include`; there is no weekday attribute, so local Saturdays (7 of 14 false alarms) could not be excluded; `GROUP_DATE` `hour` keys carry the date, so there is no hour-of-day profile; `GROUP_DATE_RANGES` returns ranges alphabetically although the skill says supplied order; and the rolling z-score has no warm-up (z = −36 on day 3) while its index sibling waits a full window.

**Source of inspiration:** the showcase examples under the `pulse-casebook` repository ([multi-time-zone ops](https://github.com/frankbardon/pulse-casebook/blob/main/showcase/multi-timezone-ops/README.md)), each a worked analysis run end to end in Pulse with a `REPRODUCE.md` of expected numbers. The items below are the gaps those runs hit. Where a fix simplifies an example, this unit's PR converts that example and refreshes its expected numbers.

Numbering note: appended after U43, not renumbered. This unit was not in the original plan. **Release intent:** v1.0.0 (owner decision 2026-10-10: Pulse should be as self-sufficient as possible at v1).

## References

**Read before starting:**
- `.claude/reference/execution-modes.md` (Date-family field types, Time zones) and `skills/time-zones.md`
- `internal/temporal` (all zone math lives there: `TestNoZoneMathOutsideTemporal`)
- `skills/op-group-date.md`, `skills/op-group-date-ranges.md`, `skills/op-attr-date-part.md`, `skills/op-overlay-zscore-vs-rolling.md`, `skills/op-test-trend.md`

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#348** (Local time) Per-row zones: zone-capable slots accept `tz: {"field": "<categorical of IANA names>"}`, and import accepts a per-row source zone column; refusals for an unknown zone name are coded per row
- [x] **#349** (Local time) Calendar parts: `ATTR_DATE_PART` `iso_weekday`, a `GROUP_DATE` `hour_of_day` component, and `day_of_week` keys in Monday→Sunday order that honour `include`. **The `weekday` NAME part was dropped by owner decision** (attributes are numeric-only; names via `GROUP_DATE day_of_week`, numbers via `iso_weekday` plus label tables / `.sav` labels)
- [x] **#350** (Local time) `GROUP_DATE_RANGES` returns ranges in supplied order in grouped Process and crosstab, as documented
- [x] **#351** (Local time) Rolling overlays take `min_periods` (default: the window), so the z-score and index siblings warm up alike
- [x] **#352** (Local time) Docs: what `--dst-policy earlier` / `later` do to a nonexistent local time (earlier lands on the later wall clock), and how `TEST_TREND` orders keys. **Shipped:** `TEST_TREND`, `Request.Sort` and window `order_by` follow a grouper's key order for grouper key columns (lexicographic only for non-grouper string columns)
- [x] **#353** (Local time) Acceptance: the multi-time-zone example's six-slot Compose views become single requests, including one warehouse × local-hour matrix, and the anomaly screen excludes local Saturdays in Pulse

## Scope

**In scope**
- Per-row zones on slots and import
- Weekday, hour-of-day and day-of-week ordering
- Date-range order
- Rolling warm-up
- DST and trend docs

**Out of scope**
- Partitioned overlays themselves ([U46](U46-grouped-analysis.md))
- Synth `datetime` ([U52](U52-synth-realism.md))

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(local-time/E<n>-S<m>): …`; close each epic with `milestone(local-time/E<n>): vertical slice complete — <epic title>`.

### E1 — One request, many zones
- S1: per-row zone resolution in `internal/temporal`; slot and import surfaces
- S2: convert the six-slot views

### E2 — Calendars read like calendars
- S1: weekday, hour-of-day, day-of-week order; date-range order
- S2: rolling `min_periods`; docs

## Acceptance criteria

- [x] A per-row-zone local-day grouping equals the six single-zone slots combined
- [x] Day-of-week keys come back Monday first; date ranges in supplied order
- [x] No zone math outside `internal/temporal`
- [x] Showcase examples simplified where this unit allows (multi-time-zone delivery operations): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse — [pulse-casebook#7](https://github.com/frankbardon/pulse-casebook/pull/7)
- [x] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump); the deliberate behaviour breaks are listed in [03-embedder-migration](../v1.0.0-api-and-release/03-embedder-migration.md) "Changes from U50"
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestNoZoneMathOutsideTemporal`, `TestUTCZoneIsIdentity`, `TestDSTBoundaries`, the time-zone parity suites, `TestPayloadSchemaGolden`
- New: per-row zone equivalence and DST tests, calendar-order tests, rolling warm-up test
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- `execution-modes.md` (Time zones) and `skills/time-zones.md`; the touched atomic skills; payload-schema golden; `make docs`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Handed on from U30

`ATTR_PREVIOUS_STATE` (#452, from #449 / E7-S2) was filed here first and moved to [U55](U55-previous-state.md) when this unit closed (see Handed on below).

## Handed on from U45

Filed from [U45](U45-binary-and-decimal.md); tick in [`TODO.md`](../TODO.md) when done.

- [x] **#467** `FilterToFileRequest` has no `TimeZone` field: a `to_day` predicate in `FilterToFile*` honours only `Options.DefaultTimeZone` (a public-API addition: the API golden, the predicate hash and the output name). Found in U45 (E4-S1)
- [x] **#468** `FILTER_DATE_RANGES` on `FilterToFileWithRequest` is refused with an UNCODED error ("untranslatable filter type", pre-existing): give it a coded refusal (or translate it). Found in U45 (E4-S1)

## Handed on from U47

Filed from [U47 handed on](U47-survey-tables.md#handed-on); tick in [`TODO.md`](../TODO.md) when done. It lands here because this unit's calendar parts (weekday, hour of day) are numeric codes whose names belong on a `.sav` as value labels.

- [x] **#470** `.sav` value labels on a NUMERIC coded field (a `u8` / `u16` holding 1 = Male, 2 = Female, or a weekday 1..7) bound to a label table: today `--labels` / `io.SPSSWriterOptions.ValueLabels` bind `categorical_*` fields only (`PULSE_LABEL_FIELD_NOT_CATEGORICAL`). Sites: `internal/io/spss/dict_labels.go` (codes = the stored integers; the table's keys are their decimal spellings), `internal/io/export.go` (`cohortValueLabels` enumerates no dictionary for a numeric field — enumerate the table instead), `types/labels.go` binding validation. Found in U47 (E4-S3)

## Human inputs & decisions

- **Decided:** a per-row zone is a SIBLING field, not a nested object: `time_zone_field` on a request and `tz_field` on a slot, naming a categorical of IANA names; `tz` and `tz_field` together are `PROCESSING_CONFIG`. Import takes `source_tz_field` (`--source-tz-field`).
- **Decided:** a null zone cell makes the local value null (grouper `n_null`, attribute / feature null, filter `n_null_input`); it never falls back to UTC. An unknown zone name is `PULSE_TIMEZONE_UNKNOWN` from predict and runtime alike.
- **Decided:** expressions (`FILTER_EXPRESSION`, `ATTR_FORMULA` date functions) read the REQUEST zone only; a per-row zone applies to the zone-capable operators.
- **Decided:** a schema-declared zone column (every request inheriting a cohort-level default zone field) is deferred to the v1.x backlog (#480): it needs a `0x02` schema section or a sidecar.
- **Decided:** an unknown zone name on import is a skipped row (`PULSE_TIMEZONE_UNKNOWN` as a row error, counted in `ROWS_DROPPED`, fatal under `--strict`); DST refusals stay fatal. A convert drops such rows at the drop stage `source`.
- **Decided (owner, 2026-10-18):** the `ATTR_DATE_PART` `weekday` NAME part is dropped: attributes are numeric-only. Names come from `GROUP_DATE day_of_week`; numbers from `iso_weekday` (Monday = 1) plus label tables or `.sav` value labels.
- **Decided:** numeric `.sav` / row-export label bindings use a new code, `PULSE_LABEL_KEY_INVALID`; `PULSE_LABEL_TABLE_NOT_ENUMERABLE` is owned `shared`.
- **Decided (owner):** extension attributes can emit null through the new optional `extend.NullableAttribute` (`EmitsNull() bool`).

## Shipped

Pulse PR [#365](https://github.com/frankbardon/pulse/pull/365); showcase conversion [pulse-casebook#7](https://github.com/frankbardon/pulse-casebook/pull/7) (`multi-timezone-ops`, one warehouse × local-hour matrix and the local-Saturday exclusion in Pulse). Epics:

- **E1 — One request, many zones:** per-row zone resolution in `internal/temporal` (`time_zone_field` / `tz_field`), every execution mode; import `source_tz_field`.
- **E2 — Calendars read like calendars:** Monday-first `day_of_week` honouring `include`, `hour_of_day`, `iso_weekday`, ISO `dow`, `GROUP_DATE_RANGES` supplied order, rolling `min_periods`, the `--dst-policy` docs.
- **E3 — Embedder edges:** `FilterToFileRequest.TimeZone` (#467), the coded `FILTER_DATE_RANGES` refusal (#468), numeric `.sav` value labels (#470).
- **E5–E7 — silent gaps closed mid-effort, by owner decision:** `TEST_TREND`, `Request.Sort` and window `order_by` follow grouper key order; `ATTR_DATE_PART` null date is null; row exports and convert refuse bad label bindings like `.sav`; `pulse cohort filter --time-zone`; the score attributes (`ATTR_ZSCORE`, `ATTR_TSCORE`, `ATTR_NORMALIZED`, `ATTR_PERCENTILE`, `ATTR_SET_POPCOUNT`) are null on a null input and on a constant field; `extend.NullableAttribute`. Also fixed: a crosstab beside `ATTR_PERCENTILE` failed `PROCESSING_INTERNAL` on the fused arm, and `ConvertJob.Hash` ignored the source-zone and DST options (a cache-key collision).

## Handed on

| Item | To | Why |
|---|---|---|
| #452 `ATTR_PREVIOUS_STATE` | [U55](U55-previous-state.md) | a new operator with its own atomic skill and `flows` declarer; it reuses this unit's per-row zones |
| #480 schema-declared zone column | v1.x backlog (no unit) | a `0x02` schema section or sidecar; interview decision |
| #481 per-row zone through joins / chain stages ≥1 | v1.x backlog (no unit) | only refusal tests exist |
| #482 z / T / normalized zero-spread silence | v1.x backlog (no unit) | consider a warning |
| #483 `Pulse.PredictConvert` + `pulse convert --labels` | v1.x backlog (no unit) | a library convert predict without a resolver cannot raise `PULSE_LABEL_KEY_INVALID` |
| #484 numeric label bindings on process / sample / facet / chain | v1.x backlog (no unit) | replace mode would turn a number into a string; design call |
