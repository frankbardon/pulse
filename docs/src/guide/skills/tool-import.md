```yaml
name: tool-import
kind: tool
description: Import a tabular source file (or pass through .pulse) into a managed handle.
type: reference
applies_to: mcp
```

## When to use

CALL TO BRING AN EXTERNAL DATA FILE INTO PULSE AS A COHORT.
CSV, TSV, NDJSON, JSON array, Parquet, Arrow, Excel, SPSS. Managed handles live under `$PULSE_DATA_DIR/imports/` with a TTL sidecar; every inspect/predict/process/sample/facet slides expiry. `.pulse` passes through (`managed=false`).

## Input

| Field | Notes |
|---|---|
| `source` (req) | path relative to `PULSE_DATA_DIR` |
| `format` | default from the extension (`.sav`/`.zsav` → `spss`) |
| `handle` | default = source basename |
| `ttl` | `24h`, `7d` (default) or `pin` (no expiry) |
| `overwrite` | default `false` → `PULSE_IMPORT_HANDLE_EXISTS` |
| `schema`, `ignore_extra`, `float` | explicit schema BY NAME; inferred float width ([`import-schema-rows`](import-schema-rows.md)) |
| `sheet` / `charset` | Excel sheet / SPSS decode override ([`spss-import-schema`](spss-import-schema.md)) |
| `groups`, `suggest_groups` | parent groups: suggest, then apply ([`cohort-parent-groups`](cohort-parent-groups.md)) |
| `vectors`, `vector_kinds` | Wide batteries fold into ONE vector, as does a native list column ([`vector-fields`](vector-fields.md)) |
| `source_tz`, `column_source_tz`, `dst_policy`, `source_tz_field` | source zones ([`import-source-zones`](import-source-zones.md)) |

## Output

`descriptor.Envelope` over `handle`, `managed_path`, `format`, `row_count`, `expires_at`, `managed`, plus — when non-empty — `promoted_fields`, `width_warnings`, `source_warnings` (`PULSE_SPSS_*`, `PULSE_IMPORT_TIMESTAMP_TRUNCATED`), `groups`, `group_warnings`, `group_candidates`, `zone_warnings`, `rows_dropped`, `dropped_warnings`.

## Gotchas

- **SPSS is schema-authoritative**: inference skipped; dictionaries hold SPSS CODES, not labels. Read every `PULSE_SPSS_*` in `source_warnings` — they change what the cohort means. No missing-value mode here; writing `.sav` is CLI-only.
- **Explicit schema matches BY NAME**, never position; each mismatch is a coded refusal before any row.
- **Naive datetimes import as UTC** unless `source_tz` names the zone; a DST gap / overlap is fatal unless `dst_policy` resolves it. Changing zone means re-import with `overwrite: true`.
- **Zero rows out of a non-empty source is FATAL `PULSE_IMPORT_ROW_ERROR`**; a partial failure warns `PULSE_IMPORT_ROWS_DROPPED`.
- A `.pulse.zst` is refused `PULSE_COHORT_COMPRESSED`. Success rebinds session-scoped tools from the new cohort (as [`tool-inspect`](tool-inspect.md)).

## See

- [`import-schema-rows`](import-schema-rows.md), [`import-source-zones`](import-source-zones.md), [`spss-cohorts`](spss-cohorts.md), [`cohort-parent-groups`](cohort-parent-groups.md), [`vector-fields`](vector-fields.md), [`tool-drop`](tool-drop.md), [`tool-imports-list`](tool-imports-list.md), `pulse_errors_lookup`.
