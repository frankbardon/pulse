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
- `charset` (string, optional): SPSS-only encoding override for a `.sav` / `.zsav` (`windows-1252`, `cp1252`, `1252` are one request); ignored for every other format. Empty keeps the file's own declaration.
- `overwrite` (bool, optional): replace existing handle. Default `false` → `PULSE_IMPORT_HANDLE_EXISTS` on collision.

## Output

`descriptor.Envelope` wrapping the import record: `handle`, `managed_path`, `format`, `row_count`, `expires_at`, `managed` (bool), plus `promoted_fields` and `source_warnings` (coded source-parse diagnostics — the `PULSE_SPSS_*` family today), both omitted when empty. Field descriptions: >1000 bytes → `PULSE_IMPORT_DESCRIPTION_TOO_LONG`; low-quality → `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (error under `--strict`).

## Gotchas

- **SPSS is schema-authoritative and import-only.** The `.sav` / `.zsav` dictionary DECLARES every column's type, nullability and categorical dictionary order, so inference is skipped entirely — sampling knobs inert, `promoted_fields` always empty. Its dictionaries hold the SPSS numeric **codes**, not the value labels (two codes may share one label, so a label-keyed dictionary would collapse them); resolve labels at output time with a LabelTable. `.sav` is writable too — `pulse export spss` / `pulse convert x.csv out.sav` — but only from the CLI, not from this tool.
- **All three `.sav` data-section encodings read.** Uncompressed, bytecode (SPSS's save default) and ZSAV zlib blocks — so a `.zsav` imports directly, no re-save. ZSAV is two layers: the blocks inflate to a bytecode stream, indexed by a `ZHEADER`/`ZTRAILER` pair that Pulse validates before inflating. A broken index is `PULSE_SPSS_ZSAV_INVALID` naming the block; a damaged block is `PULSE_SPSS_ZSAV_BLOCK_CORRUPT`; a stream that disagrees with its own dictionary is `PULSE_SPSS_COMPRESSION_INVALID` — never a silent set of wrong numbers.
- Non-fatal `PULSE_SPSS_*` diagnostics ride `source_warnings` / the envelope `warnings` array. Read them — they change what the cohort MEANS.
- **`charset` is the recourse when a `.sav` is wrong about itself.** A file that kept a stale record `7/20` name after transcoding, or declares no encoding at all and so fails the strict UTF-8 default on its first 8-bit byte, is `PULSE_SPSS_CHARSET_INVALID` / `_UNSUPPORTED` — pass `charset` and retry. Decoding only; the file's own declaration is still retained for a later export.
- **There is deliberately no missing-value mode here.** The default is the fidelity-preserving split — numeric user-missing values are null in the analytic column AND a generated `<var>_missing` sibling records why — and the only alternative drops those siblings. A knob whose sole effect is to discard information is not offered on a general import tool; ask for it explicitly with `pulse import spss --spss-missing=null`.
- **Either `.sav` byte order reads.** The header layout code decides and record `7/3` corroborates; a contradiction is a hard `PULSE_SPSS_ENDIANNESS_MISMATCH`, because byte order governs every number in the file. A zero-length source is `PULSE_SPSS_FILE_EMPTY`, distinct from `PULSE_SPSS_DICT_TRUNCATED`. An unbindable record `3`/`4` value-label set drops with `PULSE_SPSS_VALUE_LABELS_DROPPED` and the data still imports.
- Inference samples only the first N rows for nullability. A null past the sample promotes its field to nullable (`promoted_fields` + `PULSE_IMPORT_NULL_PROMOTED`) instead of failing — but an explicit schema keeps such a null as `PULSE_IMPORT_ROW_ERROR`.
- TTL `pin` is the only way to disable expiry — there is no "forever" duration.
- Pulse-format passthrough skips the copy + sidecar (`managed=false`). `pulse_drop` is a no-op on passthroughs.
- Side effect on MCP success: session-scoped tool rebinding from the new cohort (same as `tool-inspect`).

## See

- `tool-drop` — remove a managed handle.
- `tool-imports-list` — enumerate active handles.
- `cohort-schema-design` — the `.pulse` format produced by import.
- `spss-cohorts` — the full SPSS surface: type mapping, derived columns, missing-value split, metadata sidecar.
- `pulse_errors_lookup` — per-code prose + fixups for the `PULSE_SPSS_*` family.
