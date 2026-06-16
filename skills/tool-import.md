---
name: tool-import
kind: tool
description: Import a tabular source file (or pass through .pulse) into a managed handle.
type: reference
applies_to: mcp
---

## When to use

Bring an external dataset into Pulse — CSV, TSV, NDJSON, JSON array, Parquet, Arrow, Excel. Managed handles live under `$PULSE_DATA_DIR/imports/` with a TTL-tracked sidecar; every subsequent inspect/predict/process/sample/facet slides expiry forward. Pulse-format sources pass through unchanged (managed=false).

## Input

- `source` (string, required): filesystem path relative to `PULSE_DATA_DIR`.
- `format` (string, optional): override — `csv`, `tsv`, `ndjson`, `jsonarray`, `parquet`, `arrow`, `excel`, `pulse`. Default: extension-detected.
- `handle` (string, optional): managed handle name. Default: source basename without extension.
- `ttl` (string, optional): Go duration (`24h`, `30m`, `3600s`) or day form (`7d`, `30d`); `pin` disables expiry. Default `7d`.
- `sheet` (string, optional): Excel sheet name; ignored for non-Excel.
- `overwrite` (bool, optional): replace existing handle. Default `false` → `PULSE_IMPORT_HANDLE_EXISTS` on collision.

## Output

`descriptor.Envelope` wrapping the import record: `handle`, `managed_path`, `format`, `row_count`, `expires_at`, `managed` (bool). Description-quality warnings emit `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (errors under `--strict`). Description > 1000 bytes → `PULSE_IMPORT_DESCRIPTION_TOO_LONG`.

## Gotchas

- TTL `pin` is the only way to disable expiry — there is no "forever" duration.
- Pulse-format passthrough skips the copy + sidecar (`managed=false`). `pulse_drop` is a no-op on passthroughs.
- Side effect on MCP success: session-scoped tool rebinding from the new cohort (same as `tool-inspect`).

## See

- `tool-drop` — remove a managed handle.
- `tool-imports-list` — enumerate active handles.
- `cohort-schema-design` — `.pulse` format produced by import.
