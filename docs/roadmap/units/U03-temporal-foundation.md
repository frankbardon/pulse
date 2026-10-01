---
id: U03
slug: temporal-foundation
title: "All date math lives in one place, and requests can name a time zone"
track: Time zones
size: M
status: not-started
depends_on: []
soft_depends_on: [U02]
blocks: [U14]
todo_items: [66, 67, 68]
branch: temporal-foundation
---

# U03 — temporal-foundation

**Outcome:** All date math lives in one place, and requests can name a time zone.

**Track:** Time zones · **Size:** M · **Depends on:** none · **Soft:** [U02](U02-public-surface.md) · **Unblocks:** [U14](U14-zone-aware-operators.md)

## Summary

Step 0 of the time-zone theme: consolidate the six open-coded epoch-day conversions into `encoding/temporal` and add the gate that bans zone math elsewhere. Then introduce the `Zone` type (embedded tzdata, transition-table fast path) and the configuration surface (`Options.DefaultTimeZone`, `Request.TimeZone`, per-slot `tz`). No operator becomes zone-aware yet; that is U14.

## References

**Theme documents (read before starting):**
- [time-zones 00 — Design](../v1.0.0-time-zones/00-design.md) — Prerequisite: consolidate, The one adapter, Where zones are configured, Rules

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#66** (5. Time zones) Step 0: `encoding/temporal`; migrate the six open-coded epoch-day sites; `TestNoZoneMathOutsideTemporal`
- [ ] **#67** (5. Time zones) `Zone` type, embedded tzdata, transition-table fast path
- [ ] **#68** (5. Time zones) `Options.DefaultTimeZone`, `Request.TimeZone`, per-slot `tz`; `date`-field rejection

## Scope

**In scope**
- `encoding/temporal`: `DayToTime`, `TimeToDay`, `DateTimeToDay`, and the zone-aware primitives
- Migrate `feature/date_features.go`, `attribute.go`, `io/arrow/types.go`, `io/export.go`, `synth/profile.go`, `synth/distributions.go`
- Gate `TestNoZoneMathOutsideTemporal`
- `Zone` type + `time/tzdata` + transition table
- Config surface and precedence; `date`-field rejection of `tz`

**Out of scope**
- Zone-aware operators, import, output rendering (U14)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(temporal-foundation/E<n>-S<m>): …`; close each epic with `milestone(temporal-foundation/E<n>): vertical slice complete — <epic title>`.

### E1 — All epoch-day conversion goes through one package
- S1: create `encoding/temporal`; move `DateTimeToDay` behind it (keep the old name as a thin forwarder if exported)
- S2: migrate the six sites; byte-identical goldens
- S3: `TestNoZoneMathOutsideTemporal` (grep gate)

### E2 — Requests can name a zone
- S1: `Zone` type, embedded tzdata, per-zone offset-transition table with an O(1) lookup
- S2: `Options.DefaultTimeZone`, `Request.TimeZone`, per-slot `tz` params; precedence; IANA-only validation
- S3: `tz` on a `date`-field operator is `PROCESSING_CONFIG`; predict echoes the resolved zone

## Acceptance criteria

- [ ] Every golden is byte-identical after the migration
- [ ] The grep gate fails on a deliberately planted `* 86400` outside `encoding/temporal`
- [ ] `LocalDay` matches `time.In(loc)` on a property test across DST transitions in at least four zones (incl. half-hour and southern-hemisphere zones)
- [ ] `Request.TimeZone: "UTC"` and an absent zone produce identical output
- [ ] `format_version` stays "1.1"
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestNoZoneMathOutsideTemporal`
- `TestDateFieldRejectsTZ`

## Update Demand companions

- CLAUDE.md "Date-family field types" paragraph (the single-adapter rule now names `encoding/temporal`)
- Payload-schema golden (new request slots)
- `.claude/reference/update-demand.md` row for `Request.TimeZone`

## Human inputs & decisions

- None.

## Notes

- Binary size grows by about 450 KB from embedded tzdata. This is accepted in the design.
