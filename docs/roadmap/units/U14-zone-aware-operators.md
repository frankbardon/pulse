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

Make the date-family operators zone-aware through the `internal/temporal` adapter U03 landed (`Zone`, `LocalDay`, `LocalMidnightUTC`, `LocalParts`): `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, range tables and `week_start`. Adds import `--source-tz` with a DST policy, zone-aware output rendering, and predict/manifest reporting.

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
- Manifest tzdata version (predict per-slot zone echo already landed in U03)
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
- [ ] Still no zone math outside `internal/temporal` (gate from U03)
- [ ] The U03 interim refusal (non-UTC zone on a `datetime` → `PROCESSING_CONFIG`) is gone for every zone-capable operator, and its test (`TestTimeZone_NonUTCDatetimeRefusedEveryMode`) is rewritten into DST-correct expectations
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestUTCZoneIsIdentity`
- `TestDSTBoundaries`
- `TestNoZoneMathOutsideTemporal`, `TestDateFieldRejectsTZ` (both landed in U03)

## Update Demand companions

- Atomic skills for every zone-aware operator
- `skills/time-zones.md`
- `docs/src/cli/flags.md` (`--source-tz`, `--dst-policy`)
- `op-group-date-ranges.md` / `op-filter-date-ranges.md`

## Human inputs & decisions

- **Default-zone env var / CLI flag.** U03 shipped `Options.DefaultTimeZone` only. Decide whether to add a `PULSE_*` env var and a CLI default-zone flag (each would need the CLAUDE.md "Build / Env" + `session-bootstrap` companions).
- **Extension zone capability.** Extensions are not zone-capable in U03 (an explicit `tz` on one is `PROCESSING_CONFIG`). Decide here or in [U34](U34-extension-validation.md) whether registrations gain a zone declaration and public temporal helpers.

## Inherited from U03

Open items [U03](U03-temporal-foundation.md) handed forward:

- **`ParseLocal` + `Ambiguity`** move here from U03; import consumes them for `--source-tz` / `--dst-policy`.
- **Remove the U03 refusal.** Today a non-UTC zone reaching a `datetime` field — or a derived field absent from the schema — is `PROCESSING_CONFIG` (`internal/descriptor/zone_resolve.go`, rule 5). Zone-aware arithmetic replaces it; `Options.DefaultTimeZone` non-UTC is already accepted at `New`, so only the per-request refusal flips.
- **Tzdata version in the manifest.** `temporal.TZDataVersion` (the IANA release of the embedded `internal/temporal/zoneinfo.zip`, pinned to the file's hash by `TestTZDataVersion_MatchesEmbeddedZip`) already exists; only the manifest exposure remains.
- ~~**Embedded tzdata determinism.**~~ Resolved in U03 (temporal-foundation E4-S2): `LoadZone` reads only Pulse's embedded `zoneinfo.zip` via `time.LoadLocationFromTZData`, never `$ZONEINFO` or host zoneinfo (`TestLoadZone_IgnoresHostZoneinfo`); `time/tzdata` is no longer imported.
- **Stdlib `Time.ZoneBounds` quirk:** past ~2037 (the rule-extended range) it reports a leap-year year-end transition one day early; `internal/temporal`'s table builder does not trust it blindly (`TestZoneOffset_LeapYearEnd`). Keep that guard when extending the table.
- ~~**`ParseDate` pre-epoch floor edge**~~ — RESOLVED in U03 (E4-S3): pre-1970 dates and datetimes are supported end to end (`date` = signed int32 days, `datetime` = signed int64 seconds). Local-day parsing must keep flooring toward the past.
- **Predict / runtime gaps** — closed in U03 (temporal-foundation E4-S1): predict resolves a join against the joined schema, honours `DisableDefaults`, the `Validate*` entry points resolve zones, Compose / chain refusals carry `details.request` / `details.stage`, and every chain stage resolves zones before the chain gate; E4-S4 made `ValidateChain*` gate each stage after smart defaults and put the `PULSE_JOIN_TOO_MANY` rule in one helper predict, the validators and runtime `Process` share. See `.claude/reference/execution-modes.md` (Time zones).
- **`TestDateFieldRejectsTZ`** already landed in U03 — #72 keeps only `TestUTCZoneIsIdentity` (U03's request-level form is `TestTimeZone_UTCIdentity`), `TestDSTBoundaries` and the `time-zones.md` skill.
