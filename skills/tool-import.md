---
name: tool-import
kind: tool
description: Import a tabular source file (or pass through .pulse) into a managed handle.
type: reference
applies_to: mcp
---

## When to use

Bring an external dataset into Pulse — CSV, TSV, NDJSON, JSON array, Parquet, Arrow, Excel, SPSS. Managed handles live under `$PULSE_DATA_DIR/imports/` with a TTL-tracked sidecar; every inspect/predict/process/sample/facet slides expiry forward. Pulse-format sources pass through unchanged (`managed=false`).

## Input

- `source` (string, required): path relative to `PULSE_DATA_DIR`.
- `format` (string, optional): override — `csv`, `tsv`, `ndjson`, `jsonarray`, `parquet`, `arrow`, `excel`, `spss`, `pulse`. Default: extension-detected (`.sav` / `.zsav` → `spss`).
- `handle` (string, optional): managed handle name. Default: source basename without extension.
- `ttl` (string, optional): Go duration (`24h`, `30m`, `3600s`) or day form (`7d`, `30d`); `pin` disables expiry. Default `7d`.
- `sheet` (string, optional): Excel sheet name; ignored for non-Excel.
- `overwrite` (bool, optional): replace existing handle. Default `false` → `PULSE_IMPORT_HANDLE_EXISTS` on collision.

## Output

`descriptor.Envelope` wrapping the import record: `handle`, `managed_path`, `format`, `row_count`, `expires_at`, `managed` (bool), plus `promoted_fields` and `source_warnings` (coded source-parse diagnostics — the `PULSE_SPSS_*` family today), both omitted when empty. Field descriptions: >1000 bytes → `PULSE_IMPORT_DESCRIPTION_TOO_LONG`; low-quality → `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (error under `--strict`).

## Gotchas

- **SPSS is schema-authoritative and import-only.** The `.sav` / `.zsav` dictionary DECLARES every column's type, nullability and categorical dictionary order, so inference is skipped entirely — sampling knobs inert, `promoted_fields` always empty. Its dictionaries hold the SPSS numeric **codes**, not the value labels (two codes may share one label, so a label-keyed dictionary would collapse them); resolve labels at output time with a LabelTable. No `pulse export spss` — an SPSS output target returns `PULSE_SPSS_EXPORT_UNSUPPORTED`.
- **Most real `.sav` files fail today.** Bytecode compression is SPSS's save default and `.zsav` is always compressed; only the uncompressed data section is decoded, so anything else is `PULSE_SPSS_COMPRESSION_UNSUPPORTED`. Re-save with `/UNCOMPRESSED` and re-import.
- Non-fatal `PULSE_SPSS_*` diagnostics ride `source_warnings` / the envelope `warnings` array. Read them — they change what the cohort MEANS.
- Inference samples only the first N rows for nullability. A null past the sample promotes its field to nullable (`promoted_fields` + `PULSE_IMPORT_NULL_PROMOTED`) instead of failing — but an explicit schema keeps such a null as `PULSE_IMPORT_ROW_ERROR`.
- TTL `pin` is the only way to disable expiry — there is no "forever" duration.
- Pulse-format passthrough skips the copy + sidecar (`managed=false`). `pulse_drop` is a no-op on passthroughs.
- Side effect on MCP success: session-scoped tool rebinding from the new cohort (same as `tool-inspect`).

## See

- `tool-drop` — remove a managed handle.
- `tool-imports-list` — enumerate active handles.
- `cohort-schema-design` — `.pulse` format produced by import, incl. the full SPSS mapping table.
- `pulse_errors_lookup` — per-code prose + fixups for the `PULSE_SPSS_*` family.
