```yaml
name: time-zones
description: Time zones end to end — which zone a request uses, how a datetime buckets by local calendar day, import in a source zone with DST policy, native timestamps, local-offset export and the tz database version. Topical design.
type: guide
kind: design
applies_to: process, compose, predict, inspect
covers: [time_zone, tz, datetime, week_start, hour, source_tz, dst_policy]
```

# Time zones

Storage is always UTC. `datetime` = epoch SECONDS, `date` = epoch DAYS (never interchangeable). A zone only changes how an instant is READ (calendar day, wall clock) or RENDERED (export). Names are `UTC` or an exact IANA `Area/Location` from Pulse's own embedded tz database (never the host's); `EST`, `Local`, `+05:00`, `""` fail `PULSE_TIMEZONE_UNKNOWN`. The manifest carries the release as `tzdata_version`; a slot-level `zone` key marks which operators take a zone (`capable`) or follow their host (`following`).

## Which zone applies

Per slot: slot `tz` -> request `time_zone` -> `Options.DefaultTimeZone` -> UTC. Predict resolves the same way and echoes `time_zones[]` (`slot`, `tz`, `source` = slot|request|options|default). `tz` on a `date` field, or on an operator that is not zone-capable, is `PROCESSING_CONFIG`. An inherited zone on a `date` field is simply not applied (`tz: null`). A non-UTC zone on a derived field (absent from the schema, e.g. a chain stage output) is refused: "a zone cannot be applied to a derived field".

UTC, `Etc/UTC` and absent are byte-identical to the pre-zone output.

## Local-day semantics

A `datetime` is read on its LOCAL calendar day in every mode (process, stream, compose, parallel, shards, crosstab, join, chain, facet); a Berlin request's days are Berlin calendar days, DST changes included. Say so in explanations: "days are Berlin calendar days".

- `GROUP_DATE` keys are local labels. `component: hour` keys `YYYY-MM-DDTHH` local wall clock (datetime only; the skipped DST hour has no bucket, the repeated hour is one bucket). `week_start` (week only) defaults to `monday` (ISO `YYYY-Www`); any other day keys by the week's first local date.
- Range literals (inline or `RangeTables`) are LOCAL calendar days in the slot's zone; a range table has no zone of its own.
- `ATTR_DATE_PART` and `FEAT_DATE_FEATURES` accept `datetime` and read the zone's wall clock; part `hour` (and the sixth `<prefix>_hour` feature column) is datetime-only. Feature `dow` stays Sunday = 0 and ignores `week_start`.
- `OVERLAY_YOY` has no zone slot; it follows its host grouper, so an hourly host gives hourly prior-year alignment.

## Import in a source zone

A naive literal (no `Z`, no offset) is UTC unless the import names a source zone: CLI `--source-tz Zone` (or `col=Zone`, wins over the bare form), library `ImportJob.SourceTZ` / `ColumnSourceTZ`, MCP `source_tz` / `column_source_tz`. Offset and `Z` literals never move. Also on `import auto`, `import predict`, `convert`, `convert predict`; managed imports persist the zone, so changing it means re-import with overwrite.

`--dst-policy error|earlier|later` (default `error`): a wall time the zone skips is `PULSE_IMPORT_DST_NONEXISTENT`, one it shows twice is `PULSE_IMPORT_DST_AMBIGUOUS`; both name row, column, value and zone and write no file. `earlier`/`later` resolve it and warn `PULSE_IMPORT_DST_RESOLVED` with counts. Predict reports the same refusal as the run.

Native timestamps: Arrow and Parquet timestamp columns type `datetime`. Zoned Arrow types and UTC-adjusted Parquet are instants (the source zone never moves them); zone-less Arrow, non-adjusted Parquet and legacy INT96 are wall clocks (source zone applies). Sub-second values floor toward the past with `PULSE_IMPORT_TIMESTAMP_TRUNCATED`. SPSS `DATETIME` is a wall clock (source zone applies); SPSS `TIME`/`DTIME` are durations, `f64` seconds, never a datetime.

## Export in a zone

`export --tz Zone` renders each `datetime` with its local offset (`2024-03-10T08:00:00+05:30`); the stored instant is unchanged. Without it, output is canonical UTC `Z`. Sub-minute historical offsets render as `Z`. `convert` has no `--tz`.
SPSS export refuses a non-UTC `--tz` (`PULSE_SPSS_EXPORT_UNSUPPORTED`).

## Gotchas

- A DST day has 23 or 25 hours; local-day counts differ from UTC-day counts only for records near midnight.
- Zone names are case-sensitive; echoes keep your spelling.
- Zones never change stored bytes; only import interpretation of NAIVE input does.

## See

[`grouper-design`](grouper-design.md), [`type-datetime`](type-datetime.md), [`type-date`](type-date.md), [`request-envelope`](request-envelope.md), [`session-format-flags`](session-format-flags.md), [`overlay-system`](overlay-system.md).
