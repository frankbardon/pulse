---
id: U14
slug: zone-aware-operators
title: "Days, weeks and date ranges can mean local calendar days, while storage stays UTC"
track: Time zones
size: M
status: not-started
depends_on: [U03]
soft_depends_on: []
blocks: []
todo_items: [69, 70, 71, 72]
branch: zone-aware-operators
---

# U14 — zone-aware-operators

**Outcome:** Days, weeks and date ranges can mean local calendar days, while storage stays UTC.

**Track:** Time zones · **Size:** M · **Depends on:** [U03](U03-temporal-foundation.md) · **Unblocks:** —

## Summary

Make the date-family operators zone-aware through the `encoding/temporal` adapter: `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, range tables and `week_start`. Adds import `--source-tz` with a DST policy, zone-aware output rendering, and predict/manifest reporting.

## References

**Theme documents (read before starting):**
- [time-zones 00 — Design](../v1.0.0-time-zones/00-design.md) — Where zones are allowed (the boundaries), Rules, Gates

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#69** (5. Time zones) Zone-aware `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, range tables, `week_start`
- [ ] **#70** (5. Time zones) Import `--source-tz` with `--dst-policy`
- [ ] **#71** (5. Time zones) Zone-aware output rendering; predict and manifest reporting (tzdata version)
- [ ] **#72** (5. Time zones) `TestUTCZoneIsIdentity`, `TestDSTBoundaries`, `TestDateFieldRejectsTZ`; `time-zones.md` skill

## Scope

**In scope**
- Zone-aware operators and range tables
- Import `--source-tz` (global/per column) + `--dst-policy`
- Output rendering with offsets
- Predict per-slot zone; manifest tzdata version
- Identity + DST gates; `time-zones.md`

**Out of scope**
- Changing stored representation (stays UTC epoch)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(zone-aware-operators/E<n>-S<m>): …`; close each epic with `milestone(zone-aware-operators/E<n>): vertical slice complete — <epic title>`.

### E1 — Bucketing and filtering use local days
- S1: `GROUP_DATE` / `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES` + range tables
- S2: `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, `week_start`

### E2 — Zones at the import and output edges
- S1: import `--source-tz` + `--dst-policy` (default `error`, naming the row)
- S2: zone-aware rendering in export and `--json` labels; predict/manifest reporting
- S3: `TestUTCZoneIsIdentity`, `TestDSTBoundaries`; skill

## Acceptance criteria

- [ ] `tz` absent or `UTC` is byte-identical to today, at the same speed (benchmark)
- [ ] Berlin-day buckets across the March and October DST changes match `time.In` grouping
- [ ] An ambiguous local time at import fails under the default policy, naming the row
- [ ] Still no zone math outside `encoding/temporal` (gate from U03)
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestUTCZoneIsIdentity`
- `TestDSTBoundaries`
- `TestNoZoneMathOutsideTemporal` (from U03)

## Update Demand companions

- Atomic skills for every zone-aware operator
- `skills/time-zones.md`
- `docs/src/cli/flags.md` (`--source-tz`, `--dst-policy`)
- `op-group-date-ranges.md` / `op-filter-date-ranges.md`

## Human inputs & decisions

- None.
