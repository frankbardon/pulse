# Time zones — UTC inside, zones only at the edges

**Status:** done — Step 0, the `Zone` type and the configuration surface landed in [U03](../units/U03-temporal-foundation.md); zone-aware operators, import and output landed in [U14](../units/U14-zone-aware-operators.md). Agent skill: `skills/time-zones.md` · **Target:** v1.0.0

## Decided rule

> **All stored values and all internal date work are UTC.** A time zone is applied only at a small, named set of **boundaries**, through **one** adapter. No other code converts between zones.

This keeps the existing invariants intact:
- `datetime` is epoch **seconds** in UTC;
- `date` is epoch **days**;
- `DateTimeToDay` (in `internal/temporal`, forwarded by `encoding`) truncates toward the past;
- `internal/processing/date_field.go` is the single operator-boundary adapter.

Time zones extend that adapter. They don't spread through the codebase.

## Prerequisite: consolidate what is already scattered (landed in U03)

Before U03, epoch-day → calendar conversion was open-coded at more sites than first counted: `internal/processing/feature/date_features.go`, `internal/processing/attribute.go`, `internal/processing/grouper.go` (the `GROUP_DATE` math), `internal/io/arrow/types.go`, `internal/io/export.go`, `internal/io/spss/data_write.go`, `internal/io/spss/mapping.go`, `internal/synth/profile.go` and `internal/synth/distributions.go`. All were correct (all UTC), but they are exactly the pattern that would multiply once zones arrive.

**Step 0** moved every one onto a single package, **`internal/temporal`** (a stdlib + `errors` leaf; it is internal so `Zone` stays unfrozen until U14 proves its surface). Public `encoding` keeps its frozen signatures and forwards into it. The gate **`TestNoZoneMathOutsideTemporal`** is a `go/ast` walk (not grep) of every non-test file outside that package: it bans the int literal `86400`, any `SecondsPerDay` reference, `time.LoadLocation` / `LoadLocationFromTZData` / `FixedZone` / `Local`, a dot-import of `time` and importing `time/tzdata`. It deliberately does not ban `.In(` — with every means of obtaining a non-UTC location banned, only `time.UTC` can reach it, and `.In(` would also hit `reflect.Type.In`. The one allow-list entry is the `encoding.SecondsPerDay` re-export declaration.

## Where zones are allowed (the boundaries)

| Boundary | Direction | Example |
|---|---|---|
| **Import** | local wall-clock text → UTC epoch seconds | a CSV whose timestamps are New York local time: `pulse import csv --source-tz America/New_York` (or per column) |
| **Operator boundary** (bucketing / extraction) | UTC epoch seconds → local calendar day / hour / weekday | `GROUP_DATE` with slot `"tz": "Europe/Berlin"` and `component: "day"`: a "day" means a Berlin day |
| **Filter / range literals** | local calendar literal → UTC instant range | `FILTER_DATE_RANGES` with `"start": "2026-03-01"` in `tz` means Berlin midnight |
| **Output rendering** | UTC instant → local ISO-8601 string with offset | export and `--json` date labels when a `tz` is in effect |

Inside every boundary, values are UTC epoch numbers, and every operator past the boundary sees UTC epoch numbers or local *day indices*. They never see `time.Location`.

## The one adapter

`internal/temporal` (landed in U03, except `ParseLocal`):

```go
// LocalDay returns the local calendar day index for a UTC instant in zone z.
// day = floor((epochSec + offset(z, epochSec)) / 86400) — offset looked up per instant, so DST is correct.
func LocalDay(sec int64, z *Zone) int64
func LocalParts(sec int64, z *Zone) Parts                  // year, month, day, hour, weekday, iso week… for ATTR_DATE_PART / FEAT_DATE_FEATURES
func LocalMidnightUTC(day int64, z *Zone) int64            // range literal → UTC instant (start of local day)
func ParseLocal(s string, z *Zone, policy Ambiguity) (int64, error) // import — moved to U14 with Ambiguity
```

- **`*Zone`** wraps `time.Location`, is resolved once per request, and is cached per instance (`temporal.Cache`, built in `pulse.New`).
- **Bucketing cost:** a per-zone offset-transition table built from `Time.ZoneBounds` over 1900–2100 makes the lookup **allocation-free and amortized O(1)** per row (binary search plus a last-span cache; `time.In` fallback outside the window) — no `time.Time` allocation in the hot loop.
- **`tz` absent or `"UTC"`** short-circuits to today's code path, **byte-identical results and identical performance**.

## Where zones are configured

```jsonc
{ "time_zone": "America/Chicago",                      // Request-level default for every zone-capable slot
  "groups": [{ "type": "GROUP_DATE", "field": "ts", "tz": "Europe/London", "params": { "component": "week" } }] }  // per-slot override
```

`tz` is a **typed slot key** on `Group`, `Filterer`, `Attribute` and `Feature` (so also crosstab rows / columns) — never a key inside `params` — so it is schema-visible and one resolver handles every slot family. `time_zone` sits on `Request` and `FacetRequest`; Compose and Chain inherit it per inner `Request`; `SampleRequest` has none.

Precedence: per-slot `tz` → `Request.TimeZone` → `Options.DefaultTimeZone` → `"UTC"`.

**Zone-capable operators** (declared once in `internal/descriptor/capabilities_zone.go`, surfaced as the manifest `zone` key): `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES` (`capable`). **`OVERLAY_YOY` follows its host grouper's zone** (`following`) and has no `tz` of its own — a disagreeing overlay zone would be meaningless. **No windows:** no time-based window exists (`FrameSpec.Mode` is `"rows"`-only). Extension operators are not zone-capable (follow-up: U14 or U34). Range tables, whose entries are interpreted in the slot's resolved zone, are U14. ~~A table that pins its own `tz`~~ — cut in U14: tables stay calendar-only.

**Posture (since U14):** a non-UTC zone on a `datetime` schema field is applied; on a derived field (absent from the schema) it is still refused with `PROCESSING_CONFIG` rather than silently ignored. UTC and the fixed-zero `Etc/*` aliases are byte-identical to no zone.

## Rules that keep it honest

- **`date` fields ignore zones.** A `date` is already a calendar day with no instant attached. Passing an explicit `tz` to an operator over a `date` field is a validation error (`PROCESSING_CONFIG`), even `"UTC"`. An INHERITED zone (request / options) is not applied to a `date` field and predict echoes `tz: null` — that skip is by design.
- **Zone names are IANA only:** exactly `UTC` or an `Area/Location` name (`Europe/Berlin`). No abbreviations or legacy names like `EST` / `EST5EDT`, no `Local`; anything else is `PULSE_TIMEZONE_UNKNOWN`. Fixed offsets are expressed as `Etc/GMT-5`, or the explicit `"+05:00"` form for imports only (U14).
- **Embedded tzdata.** Pulse embeds its own copy of the tz database (`internal/temporal/zoneinfo.zip`) and resolves zones only from it via `time.LoadLocationFromTZData` — never `$ZONEINFO`, the host's zoneinfo files or `time/tzdata` — so results never depend on the host. This costs about 400 KB of binary size, and makes the same request give the same answer on every machine, which is part of the determinism promise. The release is recorded as `temporal.TZDataVersion`; reporting it in the manifest is U14.
- **DST at import:**
  - A non-existent local time (spring-forward gap) and an ambiguous one (fall-back overlap) follow an explicit `--dst-policy earlier | later | error`. The default is `error`, naming the row.
  - Silent shifting is never allowed.
- **Predict** echoes the resolved zone per slot (`PredictResult.TimeZones`: `{slot, operator, field_type, tz, source}`, landed in U03). Explain says "days are Berlin calendar days".
- **Weekly buckets** define the week start explicitly (`week_start: monday`, the ISO default), because week start differs by locale.

## Gates

- **`TestNoZoneMathOutsideTemporal`:** the `go/ast` gate above (U03).
- **`TestUTCZoneIsIdentity`** (+ `TestUTCZoneIsIdentity_Export` for export): `tz: "UTC"` and absent `tz` give byte-identical output across every zone-aware operator.
- **`TestDSTBoundaries`:** fixtures spanning spring-forward and fall-back in several zones (including half-hour offsets like `Asia/Kolkata` and a southern-hemisphere zone), checked against `time.In` as the reference.
- **`TestDateFieldRejectsTZ`** (U03).

## Deliverables

- [x] Step 0: `internal/temporal`; migrate every open-coded site; `TestNoZoneMathOutsideTemporal` (U03)
- [x] `Zone` type, embedded tzdata, transition-table fast path (U03)
- [x] `Options.DefaultTimeZone`, `Request.TimeZone`, per-slot `tz`; precedence; `date`-field rejection (U03)
- [x] Zone-aware `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, range tables, `week_start` (U14; plus `GROUP_DATE` `hour`)
- [x] Import `--source-tz` (global / per column) with `--dst-policy` (U14; also `import auto`, `convert`, managed imports, MCP `pulse_import`)
- [x] Zone-aware output rendering — export `--tz` local offsets (U14). `--json` date labels are the zone's local calendar labels (bucket keys); no offset form. `convert` has no `--tz`; SPSS export refuses a non-UTC `--tz`
- [x] Predict and manifest reporting — resolved zones (U03); manifest `tzdata_version` (U14)
- [x] Identity and DST gates (`TestUTCZoneIsIdentity`, `TestUTCZoneIsIdentity_Export`, `TestDSTBoundaries`); skills `op-*` params updated; topical skill `time-zones.md`

## Follow-ups after U14 (not committed to v1.0.0; additive when picked up)

- **`convert --tz`.** `convert`'s export half always renders canonical UTC (`…Z`); only `pulse export` takes `--tz`. Add the flag (and `ConvertJob.TimeZone`) reusing the export renderer (`temporal.FormatLocal`); UTC / absent must stay byte-identical.
- **SPSS local wall-clock export.** `pulse export --tz` on SPSS refuses a non-UTC zone (`PULSE_SPSS_EXPORT_UNSUPPORTED`) because `.sav` DATETIME has no offset slot. Alternative: write local wall-clock seconds and document re-import with `--source-tz`; replaces the refusal additively (manifest `export.formats[].time_zone` flips `refused` → `local_offset`).
- **Local-offset form for `--json` date labels.** `GROUP_DATE` keys are local calendar labels with no offset; `period_start` / `period_end` carry no instant. Decide whether an opt-in instant/offset rendering belongs in response shaping ([U17](../units/U17-response-shaping-core.md)) or stays out.
- **Extension zone capability** — owned by [U34](../units/U34-extension-validation.md) (Inherited from U14).
