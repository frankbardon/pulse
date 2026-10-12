---
id: U50
slug: local-time
title: "Every row is read in its own local time, and calendar parts sort and filter the way a calendar does"
track: Time zones
size: M
status: not-started
depends_on: []
soft_depends_on: [U46]
blocks: [U32]
todo_items: [348, 349, 350, 351, 352, 353]
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

- [ ] **#348** (Local time) Per-row zones: zone-capable slots accept `tz: {"field": "<categorical of IANA names>"}`, and import accepts a per-row source zone column; refusals for an unknown zone name are coded per row
- [ ] **#349** (Local time) Calendar parts: `ATTR_DATE_PART` `weekday` (and ISO weekday number), a `GROUP_DATE` `hour_of_day` component, and `day_of_week` keys in Monday→Sunday order that honour `include`
- [ ] **#350** (Local time) `GROUP_DATE_RANGES` returns ranges in supplied order in grouped Process and crosstab, as documented
- [ ] **#351** (Local time) Rolling overlays take `min_periods` (default: the window), so the z-score and index siblings warm up alike
- [ ] **#352** (Local time) Docs: what `--dst-policy earlier` / `later` do to a nonexistent local time (earlier lands on the later wall clock), and that `TEST_TREND` orders string keys lexicographically
- [ ] **#353** (Local time) Acceptance: the multi-time-zone example's six-slot Compose views become single requests, including one warehouse × local-hour matrix, and the anomaly screen excludes local Saturdays in Pulse

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

- [ ] A per-row-zone local-day grouping equals the six single-zone slots combined
- [ ] Day-of-week keys come back Monday first; date ranges in supplied order
- [ ] No zone math outside `internal/temporal`
- [ ] Showcase examples simplified where this unit allows (multi-time-zone delivery operations): requests converted, README and REPRODUCE.md expected numbers refreshed, every step still pure Pulse
- [ ] `format_version` stays `"1.1"`; every change is additive (or the unit's decision records the one deliberate bump)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestNoZoneMathOutsideTemporal`, `TestUTCZoneIsIdentity`, `TestDSTBoundaries`, the time-zone parity suites, `TestPayloadSchemaGolden`
- New: per-row zone equivalence and DST tests, calendar-order tests, rolling warm-up test
- Avoid the prefix-matched gate families for new gates unless the gate is also listed in CLAUDE.md "Non-Skippable CI Gates"

## Update Demand companions

- `execution-modes.md` (Time zones) and `skills/time-zones.md`; the touched atomic skills; payload-schema golden; `make docs`
- CLAUDE.md only where a contract it states moves (keep it ≤ 40,000 bytes)

## Human inputs & decisions

- **Open:** per-row zone as a slot `tz` object or a request-level default zone field; and whether a cohort can declare a zone column in its schema so every request inherits it.
