---
name: tool-import
kind: tool
description: Import a tabular source file (or pass through .pulse) into a managed handle.
type: reference
applies_to: mcp
---

## When to use

External data into Pulse — CSV, TSV, NDJSON, JSON array, Parquet, Arrow, Excel, SPSS. Managed handles live under `$PULSE_DATA_DIR/imports/` with a TTL sidecar; every inspect/predict/process/sample/facet slides expiry forward. `.pulse` passes through unchanged (`managed=false`).

## Input

| Field | Notes |
|---|---|
| `source` (req) | path relative to `PULSE_DATA_DIR` |
| `format` | `csv`/`tsv`/`ndjson`/`jsonarray`/`parquet`/`arrow`/`excel`/`spss`/`pulse`; default extension-detected (`.sav`/`.zsav` → `spss`) |
| `handle` | default = source basename |
| `ttl` | Go duration (`24h`) or day form (`7d`); `pin` = no expiry (the only way). Default `7d` |
| `sheet` | Excel sheet name; inert elsewhere |
| `charset` | SPSS-only decode override (`windows-1252`/`cp1252`/`1252` fold); empty keeps the file's declaration |
| `overwrite` | default `false` → `PULSE_IMPORT_HANDLE_EXISTS` |

## Output

`descriptor.Envelope` over `handle`, `managed_path`, `format`, `row_count`, `expires_at`, `managed`, plus `promoted_fields` and `source_warnings` (coded source-parse diagnostics, `PULSE_SPSS_*` today) — both omitted when empty. Description >1000 bytes → `PULSE_IMPORT_DESCRIPTION_TOO_LONG`; low-quality → `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (error under `--strict`).

## Gotchas

- **SPSS is schema-authoritative and import-only here.** Its dictionary declares type, nullability and dictionary order ⇒ inference skipped, sampling knobs inert, `promoted_fields` always empty. Dictionaries hold SPSS **codes**, not labels (two codes may share one label) — resolve labels at output via a LabelTable. Writing `.sav` is CLI-only: `pulse export spss`, `pulse convert x.csv out.sav`.
- Non-fatal `PULSE_SPSS_*` ride `source_warnings` / envelope `warnings`. Read them — they change what the cohort MEANS. Damage and self-contradiction (byte order, compression, charset) are FATAL coded refusals, never silent wrong numbers.
- `charset` is the recourse when a `.sav` is wrong about itself (stale record `7/20`; no declaration, failing the strict UTF-8 default). Decode-side only — the declaration is retained for export.
- **No missing-value mode here.** Default is the fidelity split: numeric user-missing → null + a `<var>_missing` sibling recording why. The only alternative discards those siblings; ask for it explicitly with `pulse import spss --spss-missing=null`.
- Nullability is inferred from the first N rows. A later null promotes the field (`promoted_fields` + `PULSE_IMPORT_NULL_PROMOTED`); an explicit schema raises `PULSE_IMPORT_ROW_ERROR` instead.
- **Zero rows out of a non-empty source is a FATAL `PULSE_IMPORT_ROW_ERROR`, not an empty import** — details carry `rows_read` / `rows_failed` / `first_row` / `first_error`, and no cohort is written. Export and convert mirror it with `PULSE_EXPORT_ROW_ERROR` (convert's row errors all come from the TARGET writer), and convert writes no `--keep-pulse` intermediate. PARTIAL failure is unchanged (rows land, the rest ride `row_errors`, no error), and a source with no data rows stays a legitimate empty cohort.
- Passthrough skips copy + sidecar; `pulse_drop` is a no-op on it. MCP success rebinds session-scoped tools from the new cohort (as `tool-inspect`).

## See

- `tool-drop`, `tool-imports-list`, `cohort-schema-design`.
- `spss-cohorts` — type mapping, derived columns, missing-value split, metadata sidecar, per-code prose.
- `pulse_errors_lookup`.
