```yaml
name: tool-import
kind: tool
description: Import a tabular source file (or pass through .pulse) into a managed handle.
type: reference
applies_to: mcp
```

## When to use

CALL TO BRING AN EXTERNAL DATA FILE INTO PULSE AS A COHORT.
CSV, TSV, NDJSON, JSON array, Parquet, Arrow, Excel, SPSS. Managed handles live in `$PULSE_DATA_DIR/imports/` with a TTL sidecar; each read slides the expiry. `.pulse` passes through (`managed=false`).

## Input

| Field | Notes |
|---|---|
| `source` (req) | relative to `PULSE_DATA_DIR` |
| `format` | default by extension (`.sav`/`.zsav` → `spss`) |
| `handle` | default = source basename |
| `ttl` | `24h`, `7d` (default) or `pin` (no expiry) |
| `overwrite` | default `false` → `PULSE_IMPORT_HANDLE_EXISTS` |
| `schema`, `ignore_extra`, `float` | explicit schema BY NAME; inferred float width ([`import-schema-rows`](import-schema-rows.md)) |
| `sheet` / `charset` | Excel sheet / SPSS decode override ([`spss-import-schema`](spss-import-schema.md)) |
| `groups`, `suggest_groups` | parent groups: suggest, then apply ([`cohort-parent-groups`](cohort-parent-groups.md)) |
| `vectors`, `vector_kinds` | Wide batteries fold into ONE vector, as does a native list column ([`vector-fields`](vector-fields.md)) |
| `source_tz`, `column_source_tz`, `dst_policy`, `source_tz_field`, `zone_field` | source / cohort zones ([`import-source-zones`](import-source-zones.md)) |

## Output

No envelope: `handle`, `path` (the managed `.pulse`), `format` (always `pulse`), `source_format` (as read), `managed`, `rows_imported`, `imported_at`, `expires_at` / `ttl_seconds` (absent: pinned / passthrough), plus, when non-empty, `promoted_fields`, `null_warnings`, `width_warnings`, `source_warnings` (`PULSE_SPSS_*`, `PULSE_IMPORT_TIMESTAMP_TRUNCATED`), `groups`, `group_warnings`, `group_candidates`, `zone_warnings`, `rows_dropped`, `dropped_warnings`.

## Gotchas

- **SPSS is schema-authoritative**: inference skipped; dictionaries hold SPSS CODES, not labels; a Pulse-written `.sav` restores its types. Read each `PULSE_SPSS_*` in `source_warnings`. No missing-value mode here; writing `.sav` is CLI-only.
- **Explicit schema matches BY NAME**, never position; each mismatch refused, coded, before any row.
- **Naive datetimes import as UTC** unless `source_tz` names the zone; a DST gap / overlap is fatal unless `dst_policy` resolves it. Re-import to change zone.
- **Zero rows out of a non-empty source is FATAL `PULSE_IMPORT_ROW_ERROR`**; a partial failure warns `PULSE_IMPORT_ROWS_DROPPED`.
- A `.pulse.zst` is refused `PULSE_COHORT_COMPRESSED`. Success rebinds session tools to the new cohort.

## See

- [`import-schema-rows`](import-schema-rows.md), [`import-source-zones`](import-source-zones.md), [`spss-cohorts`](spss-cohorts.md), [`cohort-parent-groups`](cohort-parent-groups.md), [`vector-fields`](vector-fields.md), [`tool-drop`](tool-drop.md), [`tool-imports-list`](tool-imports-list.md).
