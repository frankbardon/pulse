---
id: U03
slug: temporal-foundation
title: "All date math lives in one place, and requests can name a time zone"
track: Time zones
size: M
status: done
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

Step 0 of the time-zone theme: consolidate every open-coded epoch-day conversion into `internal/temporal` (public `encoding` forwards into it) and add the gate that bans zone math elsewhere. Then introduce the `Zone` type (embedded tzdata, allocation-free amortized-O(1) offset lookup) and the configuration surface (`Options.DefaultTimeZone`, `Request.TimeZone` / `FacetRequest.TimeZone`, typed per-slot `tz`). No operator becomes zone-aware yet; that is U14 — until then a non-UTC zone reaching a `datetime` field is refused rather than silently ignored.

## References

**Theme documents (read before starting):**
- [time-zones 00 — Design](../v1.0.0-time-zones/00-design.md) — Prerequisite: consolidate, The one adapter, Where zones are configured, Rules

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#66** (5. Time zones) Step 0: `internal/temporal`; migrate every open-coded epoch-day site; `TestNoZoneMathOutsideTemporal`
- [x] **#67** (5. Time zones) `Zone` type, embedded tzdata, transition-table fast path
- [x] **#68** (5. Time zones) `Options.DefaultTimeZone`, `Request.TimeZone`, per-slot `tz`; `date`-field rejection

## Scope

**In scope**
- `internal/temporal` (stdlib + `errors` leaf): `DayToTime`, `TimeToDay`, `DateTimeToDay`, `SecondsPerDay`, and the zone primitives; public `encoding` keeps its frozen signatures and forwards
- Migrate every open-coded site: `internal/processing/feature/date_features.go`, `internal/processing/attribute.go`, `internal/processing/grouper.go` (`GROUP_DATE` math), `internal/io/arrow/types.go`, `internal/io/export.go`, `internal/io/spss/data_write.go`, `internal/io/spss/mapping.go`, `internal/synth/profile.go`, `internal/synth/distributions.go`, plus `encoding/date.go` / `encoding/datetime.go` as forwarders
- Gate `TestNoZoneMathOutsideTemporal` (`go/ast`)
- `Zone` type + `time/tzdata` + transition table; `LocalDay`, `LocalMidnightUTC`, `LocalParts`
- Config surface, precedence, IANA-only validation, `date`-field rejection of `tz`, interim non-UTC refusal; predict echo

**Out of scope**
- Zone-aware operator arithmetic, import, output rendering, `ParseLocal` / `Ambiguity` (U14)
- Time-based windows (none exist; `FrameSpec.Mode` is `"rows"`-only)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(temporal-foundation/E<n>-S<m>): …`; close each epic with `milestone(temporal-foundation/E<n>): vertical slice complete — <epic title>`.

### E1 — All date math goes through `internal/temporal`
- S1: create `internal/temporal`; `encoding` forwards into it with unchanged signatures
- S2: migrate every site (list above); byte-identical goldens
- S3: `TestNoZoneMathOutsideTemporal` (`go/ast` gate)

### E2 — Pulse can load a zone and compute local days correctly
- S1: `temporal.Zone`, embedded tzdata, `LoadZone` validity rule, transition-table offset lookup
- S2: `LocalDay`, `LocalMidnightUTC`, `LocalParts`; property tests against `time.In`

### E3 — Requests can name a zone
- S1: wire slots (`time_zone`, typed slot `tz`), `Options.DefaultTimeZone`, manifest `zone` capability key
- S2: one resolver (`internal/descriptor/zone_resolve.go`) called by every request root and by predict; `PredictResult.TimeZones`
- S3: Update Demand companions and roadmap corrections

## Acceptance criteria

- [x] Every golden is byte-identical after the migration
- [x] The gate fails on a deliberately planted `86400` outside `internal/temporal` (in-memory falsification sub-test)
- [x] `LocalDay` matches `time.In(loc)` on a property test across DST transitions in at least four zones (incl. half-hour and southern-hemisphere zones)
- [x] `Request.TimeZone: "UTC"` and an absent zone produce identical output (`TestTimeZone_UTCIdentity`)
- [x] `format_version` stays "1.1"
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestNoZoneMathOutsideTemporal`
- `TestDateFieldRejectsTZ`
- `TestTimeZone_NonUTCDatetimeRefusedEveryMode`, `TestTimeZone_UTCIdentity`
- `TestZoneCapabilities_ExactSet`, `TestZoneCapabilities_EveryRegisteredName`, `TestManifest_ZoneKey`

## Update Demand companions

- CLAUDE.md "Date-family field types" paragraph (names `internal/temporal`, the gate, and the zone clause)
- `.claude/reference/execution-modes.md` (Time zones) — the long form
- Payload-schema and manifest goldens; `docs/src/contract/payload-schema.md`
- `.claude/reference/update-demand.md` rows for the zone slots, the capability table and `internal/temporal`
- Skills: the five zone-capable `op-*` skills, `op-overlay-yoy`, `request-envelope` (Time zones)

## Human inputs & decisions

- Decided in the unit interview; see Landed deviations.

## Notes

- Binary size grows by about 450 KB from embedded tzdata. This is accepted in the design.

## Landed deviations

- **Package is `internal/temporal`, not `encoding/temporal`.** `Zone` stays unfrozen until U14 proves the surface; public `encoding` is schema nouns + raw-byte primitives and forwards. The gate's only allow-list entry is the `encoding.SecondsPerDay` re-export declaration.
- **Gate technique is `go/ast`, not grep**, and it bans the means of obtaining a non-UTC location (`time.LoadLocation` / `LoadLocationFromTZData` / `FixedZone` / `Local`, `time/tzdata`, dot-import of `time`) plus `86400` / `SecondsPerDay`, rather than `.In(` (which would also hit `reflect.Type.In`).
- **Migration breadth:** every site found (including `GROUP_DATE` grouper math and both SPSS sites), not the original six.
- **Three epics, not two:** consolidate / `Zone` / config surface.
- **Typed slot `tz`, not a `params` key.** Schema-visible and uniform across `Group`, `Filterer`, `Attribute`, `Feature`; one generic resolver.
- **Narrow zone-capable set** declared once in `internal/descriptor/capabilities_zone.go` and surfaced as the manifest `zone` key: `capable` on `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`; `following` on `OVERLAY_YOY` (no `tz` of its own — it follows the host grouper's zone). Windows are excluded. Extension operators are not zone-capable.
- **`tz` is fully plumbed and validated, but a non-UTC zone reaching a `datetime` (or derived / joined) field is `PROCESSING_CONFIG`** — honours "never silently ignored" while landing the wire contract once. U14 removes the refusal. UTC and fixed-zero aliases (`Etc/UTC`, `Etc/GMT`, …) are never refused.
- **Inherited zone on a `date` field is skipped by design** (predict echoes `tz: null`); only an explicit slot `tz` on a `date` errors. `ATTR_DATE_PART` / `FEAT_DATE_FEATURES` accept only `date`, so today an explicit `tz` on them is always refused.
- **`Options.DefaultTimeZone` non-UTC is accepted at `New`** and refused per request — one less behaviour for U14 to flip. No env var or CLI flag (U14 decides).
- **Request roots:** `Request` + `FacetRequest` carry `time_zone`; Compose / Chain inherit per inner `Request`; `SampleRequest` none.
- **Predict echo** is `PredictResult.TimeZones []ResolvedZone` (`{slot, operator, field_type, tz, source}`), zone-capable slots only.
- **Lookup cost wording:** "allocation-free, amortized O(1)" (a `ZoneBounds`-built table over 1900–2100, binary search + last-span cache, `time.In` fallback outside), not "O(1)".
- **`ParseLocal` + `Ambiguity` moved to U14**, where import consumes them.
- **New error code** `PULSE_TIMEZONE_UNKNOWN`; every other refusal is `PROCESSING_CONFIG` (no code with a built-in expiry).
- **Open follow-ups, owned by [U14](U14-zone-aware-operators.md):** see that unit's "Inherited from U03".
