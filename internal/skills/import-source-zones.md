---
name: import-source-zones
description: Importing naive datetimes recorded in a local zone — source_tz, per-column and per-row (source_tz_field) zones, the DST gap / overlap policy, which native Arrow, Parquet and SPSS timestamps move, the dropped-row and zone warnings, and why a managed handle is re-imported to change zone. Topical design; request-time zones in time-zones.
type: guide
kind: design
applies_to: process, compose, predict
covers: [source_tz, column_source_tz, source_tz_field, dst_policy, --source-tz, --source-tz-field, --dst-policy, PULSE_IMPORT_DST_AMBIGUOUS, PULSE_IMPORT_DST_NONEXISTENT, PULSE_IMPORT_DST_RESOLVED, zone_warnings]
---

# Import in a source zone

A `datetime` is stored as epoch seconds, so the zone a naive source value was recorded in must be named at import; nothing after the import can recover it. Which zone a REQUEST reads days in: `time-zones`.

## Source zone

A naive literal (no `Z`, no offset) is UTC unless the import names a source zone: CLI `--source-tz Zone` (or `col=Zone`, wins over the bare form), library `ImportJob.SourceTZ` / `ColumnSourceTZ`, MCP `source_tz` / `column_source_tz`. Offset and `Z` literals never move. Per-row source zone: `--source-tz-field COL` (`ImportJob.SourceTZField`, MCP `source_tz_field`) reads each row's zone from another column (IANA or `±HH:MM`) for every naive datetime column; `column_source_tz` wins; an empty cell falls back to `source_tz`, else a row error; an unknown name skips the row with `PULSE_TIMEZONE_UNKNOWN` (fatal under `--strict`); DST refusals as below. Also on `import auto`, `import predict`, `convert`, `convert predict`; managed imports persist the zone, so changing it means re-import with overwrite.

`--dst-policy error|earlier|later` (default `error`): a wall time the zone skips is `PULSE_IMPORT_DST_NONEXISTENT`, one it shows twice is `PULSE_IMPORT_DST_AMBIGUOUS`; both name row, column, value, zone; no file written. `earlier`/`later` resolve it and warn `PULSE_IMPORT_DST_RESOLVED` with counts. `earlier` = PRE-transition offset, `later` = POST: overlap: first / second occurrence; GAP inverts — Berlin `02:30` on 2026-03-29: `earlier` = `01:30Z` = `03:30` local, `later` = `00:30Z` = `01:30` local. Predict reports the same refusal.

<!-- feature: io_format:arrow, io_format:parquet, io_format:spss -->
Native timestamps: Arrow and Parquet timestamp columns type `datetime`. Zoned Arrow types and UTC-adjusted Parquet are instants (the source zone never moves them); zone-less Arrow, non-adjusted Parquet and legacy INT96 are wall clocks (source zone applies). Sub-second values floor (`PULSE_IMPORT_TIMESTAMP_TRUNCATED`). SPSS `DATETIME` is a wall clock (source zone applies); SPSS `TIME`/`DTIME` are durations, `f64` seconds, never a datetime.
<!-- /feature -->

## Per-row zones and dropped rows

- The `source_tz_field` column imports as a normal column too; DST policy applies per row.
- A row it cannot zone — an empty cell with no `source_tz`, or an unknown name (`PULSE_TIMEZONE_UNKNOWN`) — is skipped, not fatal: `rows_dropped` with details `row` and `zone_column` (fatal under `--strict`).
- A missing zone column, or one typed `datetime`, is `SERVICE_VALIDATION`.
- `date` columns are never zoned; a `col=Zone` naming one is `CLI_INPUT`.

## On the managed import

<!-- feature: capability:import -->
`pulse_import` takes `source_tz`, `column_source_tz`, `dst_policy` and `source_tz_field`; resolved DST warnings ride its `zone_warnings`, skipped rows its `rows_dropped`.
<!-- /feature -->
The zone is kept on the managed sidecar, so a handle read in one zone is never reused for another: re-import with `overwrite: true` to change it.

## See

- `time-zones`, `cohort-schema-design`<!-- feature: capability:import -->, `tool-import`<!-- /feature -->
