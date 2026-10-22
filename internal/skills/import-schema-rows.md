---
name: import-schema-rows
description: Importing a tabular source — an explicit schema matched by column name and its refusal codes, what inference decides (float width, nullability, widths, blank cells), zero-row and partial row failures, format dispatch, compressed and .pulse sources, and the one JSON casing. Topical design; the managed-import tool's own arguments in its atomic skill.
type: guide
kind: design
applies_to: process, compose, predict
covers: [import, --schema, explicit schema, ignore_extra, --float, row errors, PULSE_IMPORT_ROWS_DROPPED, PULSE_IMPORT_ROW_ERROR, PULSE_IMPORT_FORMAT_UNKNOWN, PULSE_COHORT_COMPRESSED, passthrough]
---

# Import: explicit schema, inference, row failures

The same rules hold for every import path: CLI `pulse import <fmt>` / `import auto` / `import predict` / `convert`, the library `ImportJob` / `ConvertJob` / `ImportSpec`<!-- feature: capability:import -->, and `pulse_import`<!-- /feature -->. Types and widths: `cohort-schema-design`;<!-- feature: io_format:spss --> SPSS: `spss-import-schema`;<!-- /feature --> zones: `import-source-zones`; vector fields: `vector-fields`; parent groups: `cohort-parent-groups`.

## An explicit schema matches BY NAME

`[{name, type, nullable, description}]` (`precision` / `scale` for `decimal128`; `dim` REQUIRED plus `labels` / `kind` / `scale_min` / `scale_max` for `vec_*`) — the `--schema` document, the `Schema` field, the MCP `schema` argument. Omit it to infer.

- Fields match source columns by exact spelling, in any order, never by position; the array's order is the cohort's.
- Refused before any row is read: a field with no column `PULSE_IMPORT_SCHEMA_FIELD_MISSING`; a column no field names `PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED`; a repeated name `PULSE_IMPORT_SCHEMA_DUPLICATE`; an NDJSON / JSON-array key first seen after the first object (whose keys are the columns) `PULSE_IMPORT_SCHEMA_KEY_LATE`; an unknown type name `PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN` (never read as f64); an unknown entry key, an empty array or a schema on a `.pulse` source `PULSE_IMPORT_SCHEMA_INVALID`.
- `ignore_extra` (`--ignore-extra`) drops unnamed columns and late keys instead of refusing.
- So a narrowed output (a projected `--stream`) is never re-imported with its columns silently shifted.
- Not kept on the managed sidecar: the cohort header records the schema.
- Declared nullability and widths are contracts: a null in a non-nullable field, or an overflow, is a per-row `PULSE_IMPORT_ROW_ERROR`.

## What inference decides

- Floats infer `f64` — decimals stay exact, money sums never drift. `float: "f32"` (`--float f32`) halves the bytes for the whole import, rounding to ~7 digits; any other value is `PULSE_IMPORT_FLOAT_WIDTH_INVALID`.
- Nullability comes from the first sampled rows; a later null promotes the field (`promoted_fields` + `PULSE_IMPORT_NULL_PROMOTED`; `cohort-null-states`).
- Widths: a value past the sampled width promotes the field to the narrowest type holding it — never a dropped row — with one `width_warnings` entry (`PULSE_IMPORT_WIDTH_PROMOTED`: `field`, `from`, `to`, `source_row`; `cohort-width-overflow`).
- A blank categorical cell: `csv` / `tsv` / `excel` read it as NULL (one spelling for both — a documented gap that always collapses toward null); `ndjson` / `jsonarray` / `arrow` / `parquet` carry their own null channel, so `""` is an empty-string VALUE and only `null` is null. A blank numeric or date cell is null everywhere.
- SPSS is schema-authoritative: inference and the sampling knobs are skipped, `promoted_fields` stays empty.
- A field description over 1000 bytes is `PULSE_IMPORT_DESCRIPTION_TOO_LONG`; an empty, sub-10-character or generic one ("n/a", "tbd", "unknown", "field", "data", "value", "column") warns `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (an error under `--strict`).

## Row failures

- **Zero rows out of a non-empty source is FATAL `PULSE_IMPORT_ROW_ERROR`, not an empty import**: details `rows_read`, `rows_failed`, `first_row`, `first_error`; no cohort is written. Export and convert mirror it with `PULSE_EXPORT_ROW_ERROR` (convert's uncoded row errors come from the TARGET writer), and convert writes no `--keep-pulse` intermediate.
- A PARTIAL failure imports the good rows and warns `PULSE_IMPORT_ROWS_DROPPED` (details `dropped`, `imported`, `first_failure {row, column, reason}`, plus `stage` `source` / `target` / `keep_pulse` on convert) — fatal under CLI `--strict` (exit 1) or `Options.Strict`, writing nothing.
- A source with no data rows is a legitimate empty cohort.

## Dispatch and sources

- One dispatch: detection and reader construction go through the `io` factory (`io.FormatFromPath` → `io.NewReader`), the same one the CLI and embedders use. No extension match and no `format` → `PULSE_IMPORT_FORMAT_UNKNOWN`; an explicit `format` outside the list → `PULSE_IO_FORMAT_UNSUPPORTED`.
- A `.pulse.zst` (zstd transfer artifact) is refused `PULSE_COHORT_COMPRESSED`, even renamed `.pulse`; decompressing is `pulse import transfer` (CLI only; `cohort-sharding`).
- A `.pulse` source passes through: no copy, no sidecar (`managed: false`).
<!-- feature: capability:materialize, capability:process -->
- A window, feature or attribute column is not an import job: materialize it (`derived-cohorts`) — exact and atomic; never re-import a streamed result.
<!-- /feature -->
- One casing: every CLI `import <fmt>` / `import predict` / `convert` / `export` / `shard` `--json` `data` uses snake_case keys (`rows_imported`, `schema.fields[].type` as a name like `"f64"`).

## See

- `cohort-schema-design`, `cohort-null-states`, `cohort-width-overflow`, `spss-import-schema`, `import-source-zones`, `vector-fields`, `cohort-parent-groups`<!-- feature: capability:import -->, `tool-import`<!-- /feature -->
