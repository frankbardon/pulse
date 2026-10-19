---
name: time-zones
description: Time zones end to end — which zone a request uses, how a datetime buckets by local calendar day, import in a source zone with DST policy, native timestamps, local-offset export and the tz database version. Topical design.
type: guide
kind: design
applies_to: process, compose, predict, inspect
covers: [time_zone, tz, tz_field, time_zone_field, datetime, week_start, hour, source_tz, source_tz_field, dst_policy]
---

# Time zones

Storage is always UTC. `datetime` = epoch SECONDS, `date` = epoch DAYS (never interchangeable). A zone only changes how an instant is READ (calendar day, wall clock) or RENDERED (export). Names are `UTC` or an exact IANA `Area/Location` from Pulse's own embedded tz database (never the host's); `EST`, `Local`, `+05:00`, `""` fail `PULSE_TIMEZONE_UNKNOWN`. The manifest carries the release as `tzdata_version`; a slot-level `zone` key marks which operators take a zone (`capable`) or follow their host (`following`).

## Which zone applies

Per slot: slot `tz` -> slot `tz_field` -> request `time_zone_field` -> request `time_zone` -> `Options.DefaultTimeZone` -> UTC. Predict resolves the same way and echoes `time_zones[]` (`slot`, `tz`, `source` = slot|request|options|default|field). `tz` on a `date` field, or on an operator that is not zone-capable, is `PROCESSING_CONFIG`. An inherited zone on a `date` field is simply not applied (`tz: null`). A non-UTC zone on a derived field (absent from the schema, e.g. a chain stage output) is refused: "a zone cannot be applied to a derived field".

UTC, `Etc/UTC` and absent are byte-identical to the pre-zone output.

## Per-row zones

When each record has its own zone (a warehouse, a store), name a `categorical_*` field of IANA names: slot `tz_field` on `groups` / `filterers` / `attributes` / `features` (and crosstab axes), or request-wide `time_zone_field` on `Request` / `FacetRequest`. `tz` + `tz_field` on one slot is `PROCESSING_CONFIG`; so is a zone field that is not `categorical_*`, one on a non-capable operator, or an explicit `tz_field` on a `date` field. Predict checks every dictionary entry (`PULSE_TIMEZONE_UNKNOWN`, `details.dictionary_index`), as the run does. Echo: `tz: null`, `source: "field"`.

A NULL zone cell (or an out-of-dictionary code) gives the row NO local value, never a UTC fallback: groupers count it in `n_null` (no bucket).
<!-- feature: ATTR_DATE_PART, FEAT_DATE_FEATURES -->
`ATTR_DATE_PART` / `FEAT_DATE_FEATURES` output null for it.
<!-- /feature -->
<!-- feature: FILTER_DATE_RANGES -->
`FILTER_DATE_RANGES` drops it and counts it in `n_null_input`.
<!-- /feature -->
<!-- feature: OVERLAY_YOY -->
`OVERLAY_YOY` follows its host's keys.
<!-- /feature -->
Expressions (`to_day`, `weekday`) stay request-zone only (`time_zone`), never per-row: filter per row through a derived date feature plus a filter.

## Local-day semantics

A `datetime` is read on its LOCAL calendar day in every mode (process, stream, compose, parallel, shards, crosstab, join, chain, facet); a Berlin request's days are Berlin calendar days, DST changes included. Say so: "days are Berlin calendar days".

<!-- feature: GROUP_DATE -->
- `GROUP_DATE` keys are local labels. `component: hour` keys `YYYY-MM-DDTHH` (`hour_of_day`: `HH`) local wall clock (datetime only; the skipped DST hour has no bucket, the repeated hour is one bucket). `week_start` (week only) defaults to `monday` (ISO `YYYY-Www`); any other day keys by the week's first local date.
<!-- /feature -->
<!-- feature: GROUP_DATE_RANGES, FILTER_DATE_RANGES -->
- Range literals (inline or `RangeTables`) are LOCAL calendar days in the slot's zone; a range table has no zone of its own.
<!-- /feature -->
<!-- feature: FILTER_DATE_RANGES -->
- Filter-to-file zones `FILTER_DATE_RANGES` like `Process`; the zone keys the output name.
<!-- /feature -->
<!-- feature: ATTR_DATE_PART, FEAT_DATE_FEATURES -->
- `ATTR_DATE_PART` and `FEAT_DATE_FEATURES` accept `datetime` and read the zone's wall clock; part `hour` (and the sixth `<prefix>_hour` feature column) is datetime-only. `iso_weekday` / `dow` are ISO (Monday = 1), ignoring `week_start`.
<!-- /feature -->
- Formulas and expression filters: `to_day(ts)` reads the inherited zone (request `time_zone` -> `Options.DefaultTimeZone` -> UTC) on every entry point (filter-to-file: `FilterToFileRequest.TimeZone`); they take no slot `tz` (`calendar-formulas`).
<!-- feature: OVERLAY_YOY -->
- `OVERLAY_YOY` has no zone slot; it follows its host grouper, so an hourly host gives hourly prior-year alignment.
<!-- /feature -->

## Import in a source zone

A naive literal (no `Z`, no offset) is UTC unless the import names a source zone: CLI `--source-tz Zone` (or `col=Zone`, wins over the bare form), library `ImportJob.SourceTZ` / `ColumnSourceTZ`, MCP `source_tz` / `column_source_tz`. Offset and `Z` literals never move. Per-row source zone: `--source-tz-field COL` (`ImportJob.SourceTZField`, MCP `source_tz_field`) reads each row's zone from another column (IANA or `±HH:MM`) for every naive datetime column; `column_source_tz` wins; an empty cell falls back to `source_tz`, else a row error; an unknown name skips the row with `PULSE_TIMEZONE_UNKNOWN` (fatal under `--strict`); DST refusals as below. Also on `import auto`, `import predict`, `convert`, `convert predict`; managed imports persist the zone, so changing it means re-import with overwrite.

`--dst-policy error|earlier|later` (default `error`): a wall time the zone skips is `PULSE_IMPORT_DST_NONEXISTENT`, one it shows twice is `PULSE_IMPORT_DST_AMBIGUOUS`; both name row, column, value, zone; no file written. `earlier`/`later` resolve it and warn `PULSE_IMPORT_DST_RESOLVED` with counts. `earlier` = PRE-transition offset, `later` = POST: overlap: first / second occurrence; GAP inverts — Berlin `02:30` on 2026-03-29: `earlier` = `01:30Z` = `03:30` local, `later` = `00:30Z` = `01:30` local. Predict reports the same refusal.

<!-- feature: io_format:arrow, io_format:parquet, io_format:spss -->
Native timestamps: Arrow and Parquet timestamp columns type `datetime`. Zoned Arrow types and UTC-adjusted Parquet are instants (the source zone never moves them); zone-less Arrow, non-adjusted Parquet and legacy INT96 are wall clocks (source zone applies). Sub-second values floor (`PULSE_IMPORT_TIMESTAMP_TRUNCATED`). SPSS `DATETIME` is a wall clock (source zone applies); SPSS `TIME`/`DTIME` are durations, `f64` seconds, never a datetime.
<!-- /feature -->

## Export in a zone

`export --tz Zone` renders each `datetime` with its local offset (`2024-03-10T08:00:00+05:30`); the stored instant is unchanged. Default: UTC `Z`. `convert` has no `--tz`.
<!-- feature: io_format:spss -->
SPSS export refuses a non-UTC `--tz` (`PULSE_SPSS_EXPORT_UNSUPPORTED`).
<!-- /feature -->

## Gotchas

- A DST day has 23 or 25 hours; local-day counts differ from UTC-day counts only for records near midnight.

## See

`grouper-design`, `calendar-formulas`, `type-datetime`, `type-date`, `request-envelope`, `session-format-flags`, `overlay-system`.
