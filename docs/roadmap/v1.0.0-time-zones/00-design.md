# Time zones — UTC inside, zones only at the edges

**Status:** proposal · **Target:** v1.0.0

## Decided rule

> **All stored values and all internal date work are UTC.** A time zone is applied only at a small, named set of **boundaries**, through **one** adapter. No other code converts between zones.

This keeps the existing invariants intact:
- `datetime` is epoch **seconds** in UTC;
- `date` is epoch **days**;
- `encoding.DateTimeToDay` truncates toward the past;
- `processing/date_field.go` is the single operator-boundary adapter.

Time zones extend that adapter. They don't spread through the codebase.

## Prerequisite: consolidate what is already scattered

Today, epoch-day → calendar conversion is open-coded at six sites:

| Site | Code |
|---|---|
| `processing/feature/date_features.go:100` | `time.Unix(int64(v)*86400, 0).UTC()` |
| `processing/attribute.go:462` | same |
| `io/arrow/types.go:291` | same |
| `io/export.go:585` | same |
| `synth/profile.go:1789` | same |
| `synth/distributions.go:787` | local `day` constant |

These are correct (all UTC), but they are exactly the pattern that would multiply once zones arrive. **Step 0** therefore moves them all onto a single `encoding` temporal package (`encoding/temporal`): `DayToTime`, `TimeToDay`, `DateTimeToDay`, and the new zone-aware variants. It adds a grep gate, in the spirit of the existing "no call site open-codes `/ 86400`" rule, that fails on `86400`, `time.Unix(…*86400`, `.In(`, `time.LoadLocation` or `time.FixedZone` anywhere outside that package.

## Where zones are allowed (the boundaries)

| Boundary | Direction | Example |
|---|---|---|
| **Import** | local wall-clock text → UTC epoch seconds | a CSV whose timestamps are New York local time: `pulse import csv --source-tz America/New_York` (or per column) |
| **Operator boundary** (bucketing / extraction) | UTC epoch seconds → local calendar day / hour / weekday | `GROUP_DATE {"interval": "day", "tz": "Europe/Berlin"}`: a "day" means a Berlin day |
| **Filter / range literals** | local calendar literal → UTC instant range | `FILTER_DATE_RANGES` with `"start": "2026-03-01"` in `tz` means Berlin midnight |
| **Output rendering** | UTC instant → local ISO-8601 string with offset | export and `--json` date labels when a `tz` is in effect |

Inside every boundary, values are UTC epoch numbers, and every operator past the boundary sees UTC epoch numbers or local *day indices*. They never see `time.Location`.

## The one adapter

`encoding/temporal`:

```go
// LocalDay returns the local calendar day index for a UTC instant in zone z.
// day = floor((epochSec + offset(z, epochSec)) / 86400) — offset looked up per instant, so DST is correct.
func LocalDay(epochSec int64, z *Zone) int32
func LocalParts(epochSec int64, z *Zone) Parts            // year, month, day, hour, weekday, iso week… for ATTR_DATE_PART / FEAT_DATE_FEATURES
func LocalMidnightUTC(day int32, z *Zone) int64            // range literal → UTC instant (start of local day)
func ParseLocal(s string, z *Zone, policy Ambiguity) (int64, error) // import
```

- **`*Zone`** wraps `time.Location`, is resolved once per request, and is cached per instance.
- **Bucketing cost:** a per-zone offset-transition table makes it O(1) per row (no `time.Time` allocation in the hot loop).
- **`tz` absent or `"UTC"`** short-circuits to today's code path, **byte-identical results and identical performance**.

## Where zones are configured

```jsonc
{ "time_zone": "America/Chicago",                      // Request-level default for every zone-aware slot
  "groups": [{ "type": "GROUP_DATE", "field": "ts", "params": { "interval": "week", "tz": "Europe/London" } }] }  // per-slot override
```

Precedence: per-slot `tz` → `Request.TimeZone` → `Options.DefaultTimeZone` → `"UTC"`.

**Zone-aware operators:** `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, and any time-based window. Plus range tables, whose entries are interpreted in the request's zone; a table may also pin its own `tz`.

## Rules that keep it honest

- **`date` fields ignore zones.** A `date` is already a calendar day with no instant attached. Passing `tz` to an operator over a `date` field is a validation error (`PROCESSING_CONFIG`). It is never silently ignored.
- **Zone names are IANA only** (`Europe/Berlin`). No abbreviations like `EST`, which are ambiguous. Fixed offsets are expressed as `Etc/GMT-5`, or the explicit `"+05:00"` form for imports only.
- **Embedded tzdata.** Pulse imports `time/tzdata`, so results never depend on the host's zoneinfo files. This costs about 450 KB of binary size, and makes the same request give the same answer on every machine, which is part of the determinism promise. The tzdata version is reported in the manifest.
- **DST at import:**
  - A non-existent local time (spring-forward gap) and an ambiguous one (fall-back overlap) follow an explicit `--dst-policy earlier | later | error`. The default is `error`, naming the row.
  - Silent shifting is never allowed.
- **Predict** echoes the resolved zone per slot. Explain says "days are Berlin calendar days".
- **Weekly buckets** define the week start explicitly (`week_start: monday`, the ISO default), because week start differs by locale.

## Gates

- **`TestNoZoneMathOutsideTemporal`:** the grep gate above.
- **`TestUTCZoneIsIdentity`:** `tz: "UTC"` and absent `tz` give byte-identical goldens.
- **`TestDSTBoundaries`:** fixtures spanning spring-forward and fall-back in several zones (including half-hour offsets like `Asia/Kolkata` and a southern-hemisphere zone), checked against `time.In` as the reference.
- **`TestDateFieldRejectsTZ`.**

## Deliverables

- [ ] Step 0: `encoding/temporal`; migrate the six open-coded sites; `TestNoZoneMathOutsideTemporal`
- [ ] `Zone` type, embedded tzdata, transition-table fast path
- [ ] `Options.DefaultTimeZone`, `Request.TimeZone`, per-slot `tz`; precedence; `date`-field rejection
- [ ] Zone-aware `GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`, `ATTR_DATE_PART`, `FEAT_DATE_FEATURES`, `OVERLAY_YOY`, range tables, `week_start`
- [ ] Import `--source-tz` (global / per column) with `--dst-policy`
- [ ] Zone-aware output rendering (export, `--json` labels)
- [ ] Predict and manifest reporting (resolved zones, tzdata version)
- [ ] Identity and DST gates; skills `op-*` params updated; topical skill `time-zones.md`
