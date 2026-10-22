```yaml
name: time-zones
description: Time zones end to end — which zone a request uses, how a datetime buckets by local calendar day, import in a source zone with DST policy, native timestamps, local-offset export and the tz database version. Topical design.
type: guide
kind: design
applies_to: process, compose, predict, inspect
covers: [time_zone, tz, tz_field, time_zone_field, datetime, week_start, hour, source_tz, source_tz_field, dst_policy]
```

# Time zones

Storage is always UTC. `datetime` = epoch SECONDS, `date` = epoch DAYS (never interchangeable). A zone only changes how an instant is READ (calendar day, wall clock) or RENDERED (export). Names are `UTC` or an exact IANA `Area/Location` from Pulse's own embedded tz database (never the host's); `EST`, `Local`, `+05:00`, `""` fail `PULSE_TIMEZONE_UNKNOWN`. The manifest carries the release as `tzdata_version`; a slot-level `zone` key marks which operators take a zone (`capable`) or follow their host (`following`).

## Which zone applies

Per slot: slot `tz` -> slot `tz_field` -> request `time_zone_field` -> request `time_zone` -> `Options.DefaultTimeZone` -> UTC. Predict resolves the same way and echoes `time_zones[]` (`slot`, `tz`, `source` = slot|request|options|default|field). `tz` on a `date` field, or on an operator that is not zone-capable, is `PROCESSING_CONFIG`. An inherited zone on a `date` field is simply not applied (`tz: null`). A non-UTC zone on a derived field (absent from the schema, e.g. a chain stage output) is refused: "a zone cannot be applied to a derived field".

UTC, `Etc/UTC` and absent are byte-identical to the pre-zone output.

## Per-row zones

When each record has its own zone (a warehouse, a store), name a `categorical_*` field of IANA names: slot `tz_field` on `groups` / `filterers` / `attributes` / `features` (and crosstab axes), or request-wide `time_zone_field` on `Request` / `FacetRequest`. `tz` + `tz_field` on one slot is `PROCESSING_CONFIG`; so is a zone field that is not `categorical_*`, one on a non-capable operator, or an explicit `tz_field` on a `date` field. Predict checks every dictionary entry (`PULSE_TIMEZONE_UNKNOWN`, `details.dictionary_index`), as the run does. Echo: `tz: null`, `source: "field"`.

A NULL zone cell (or an out-of-dictionary code) gives the row NO local value, never a UTC fallback: groupers count it in `n_null` (no bucket).
`ATTR_DATE_PART` / `FEAT_DATE_FEATURES` output null for it.
`FILTER_DATE_RANGES` drops it and counts it in `n_null_input`.
`OVERLAY_YOY` follows its host's keys.
Expressions (`to_day`, `weekday`) stay request-zone only (`time_zone`), never per-row: filter per row through a derived date feature plus a filter.

## Stored instants

A `datetime` stores whole seconds since the epoch, naive UTC. A fraction floors toward the past (native Arrow / Parquet timestamps warn `PULSE_IMPORT_TIMESTAMP_TRUNCATED`); keep microseconds in a `u64`. An offset-bearing literal is normalised to the same instant and the offset discarded (`…T10:11:12+02:00` → `…T08:11:12Z`); export `--tz` renders it back with a local offset. Truncating to a day floors toward the past (`1969-12-31T23:59:59Z` is day −1 in UTC).

## Local-day semantics

A `datetime` is read on its LOCAL calendar day in every mode (process, stream, compose, parallel, shards, crosstab, join, chain, facet); a Berlin request's days are Berlin calendar days, DST changes included. Say so: "days are Berlin calendar days".

- `GROUP_DATE` keys are local labels. `component: hour` keys `YYYY-MM-DDTHH` (`hour_of_day`: `HH`) local wall clock (datetime only; the skipped DST hour has no bucket, the repeated hour is one bucket). `week_start` (week only) defaults to `monday` (ISO `YYYY-Www`); any other day keys by the week's first local date.
- Range literals (inline or `RangeTables`) are LOCAL calendar days in the slot's zone; a range table has no zone of its own.
- Filter-to-file zones `FILTER_DATE_RANGES` like `Process`; the zone keys the output name.
- `ATTR_DATE_PART` and `FEAT_DATE_FEATURES` accept `datetime` and read the zone's wall clock; part `hour` (and the sixth `<prefix>_hour` feature column) is datetime-only. `iso_weekday` / `dow` are ISO (Monday = 1), ignoring `week_start`.
- Formulas and expression filters: `to_day(ts)` reads the inherited zone (request `time_zone` -> `Options.DefaultTimeZone` -> UTC) on every entry point (filter-to-file: `FilterToFileRequest.TimeZone`); they take no slot `tz` ([`calendar-formulas`](calendar-formulas.md)).
- `OVERLAY_YOY` has no zone slot; it follows its host grouper, so an hourly host gives hourly prior-year alignment.

## Import in a source zone

A naive literal (no `Z`, no offset) imports as UTC unless the import names a source zone — per file, per column or per row (`source_tz_field`); a DST gap or overlap is fatal unless `dst_policy` resolves it; zoned Arrow / UTC-adjusted Parquet timestamps never move. Every rule and code: [`import-source-zones`](import-source-zones.md).

## Export in a zone

`export --tz Zone` renders each `datetime` with its local offset (`2024-03-10T08:00:00+05:30`); the stored instant is unchanged. Default: UTC `Z`. `convert` has no `--tz`.
SPSS export refuses a non-UTC `--tz` (`PULSE_SPSS_EXPORT_UNSUPPORTED`).

## Gotchas

- A DST day has 23 or 25 hours; local-day counts differ from UTC-day counts only for records near midnight.

## See

[`import-source-zones`](import-source-zones.md), [`grouper-design`](grouper-design.md), [`calendar-formulas`](calendar-formulas.md), [`type-datetime`](type-datetime.md), [`type-date`](type-date.md), [`request-envelope`](request-envelope.md), [`session-format-flags`](session-format-flags.md), [`overlay-system`](overlay-system.md).
