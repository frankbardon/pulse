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
| `format` | `csv`/`tsv`/`ndjson`/`jsonarray`/`parquet`/`arrow`/`excel`/`spss`/`pulse`; default extension-detected (`.sav`/`.zsav` → `spss`) |
| `handle` | default = source basename |
| `ttl` | Go duration (`24h`) or day form (`7d`); `pin` = no expiry (the only way). Default `7d` |
| `sheet` | Excel sheet name; inert elsewhere |
| `charset` | SPSS-only decode override (`windows-1252`/`cp1252`/`1252` fold); empty keeps the file's declaration |
| `overwrite` | default `false` → `PULSE_IMPORT_HANDLE_EXISTS` |
| `groups` | parent groups, `[{key: [...], members: [...]}]` (omit `key` for a plain tuple group); unknown entry key → `PULSE_GROUP_DECLARATION_INVALID` |
| `suggest_groups` | also return measured `group_candidates`; one extra full pass, declares nothing |
| `vectors` | native vector fields, CLI `--vector` strings: `"v:/^rate_(.*)$/"` (source order, capture group = labels) or `"v:a,b,c"` (listed order) fold sibling columns; bare `"v"` reads the native list column `v`; `"v:f32:..."` / `"v:f32"` for `vec_f32` |
| `vector_kinds` | `"v:scale:1:7"` — kind (`measure`/`scale`/`composition`/`binary`) + optional range; never inferred |
| `schema` | explicit schema `[{name, type, nullable, description}]` (`precision`/`scale` for `decimal128`; `dim` REQUIRED + `labels`/`kind`/`scale_min`/`scale_max` for `vec_*`) — the CLI `--schema` document; omit to infer |
| `ignore_extra` | with `schema`: drop columns / late keys it does not name instead of refusing |
| `float` | inferred float width: `f64` (default) or `f32`; else `PULSE_IMPORT_FLOAT_WIDTH_INVALID` |
| `source_tz` / `column_source_tz` / `dst_policy` | zone naive datetimes were recorded in (IANA, `UTC`, `±HH:MM`), per-column `{col: zone}` override, DST policy `error`/`earlier`/`later`; kept on the sidecar |

## Output

`descriptor.Envelope` over `handle`, `managed_path`, `format`, `row_count`, `expires_at`, `managed`, plus `promoted_fields`, `width_warnings` and `source_warnings` (coded source-parse diagnostics: `PULSE_SPSS_*`, `PULSE_IMPORT_TIMESTAMP_TRUNCATED`), `groups` (per-group verdict, ratio, resident dictionary bytes, byte delta), `group_warnings` (coded `{code, message, details}`), `group_candidates`, `zone_warnings`, and `rows_dropped` + `dropped_warnings` (`PULSE_IMPORT_ROWS_DROPPED`) — all omitted when empty. Description >1000 bytes → `PULSE_IMPORT_DESCRIPTION_TOO_LONG`; low-quality → `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (error under `--strict`).

## Gotchas

- **SPSS is schema-authoritative and import-only here.** Its dictionary declares type, nullability and dictionary order ⇒ inference skipped, sampling knobs inert, `promoted_fields` always empty. Dictionaries hold SPSS **codes**, not labels (two codes may share one label) — resolve labels at output via a LabelTable. Writing `.sav` is CLI-only: `pulse export spss`, `pulse convert x.csv out.sav`.
- Non-fatal `PULSE_SPSS_*` ride `source_warnings` / envelope `warnings`. Read them — they change what the cohort MEANS. Damage and self-contradiction (byte order, compression, charset) are FATAL coded refusals, never silent wrong numbers.
- `charset` is the recourse when a `.sav` is wrong about itself (stale record `7/20`; no declaration, failing the strict UTF-8 default). Decode-side only — the declaration is retained for export.
- **No missing-value mode here.** Default is the fidelity split: numeric user-missing → null + a `<var>_missing` sibling recording why. The only alternative discards those siblings; ask for it explicitly with `pulse import spss --spss-missing=null`.
- **A blank categorical cell is not always a null.** `ndjson` / `jsonarray` / `arrow` / `parquet` carry the source's own null channel, so JSON `""` (or a set validity bit) imports as an empty-string VALUE and only `null` imports as null. `csv` / `tsv` / `excel` have one spelling for both and import a blank as NULL — a documented gap, and the collapse always goes toward null. A blank in a numeric / date column is a null on every format.
- **An explicit schema matches columns BY NAME, never position** (`schema` here; CLI `--schema` on `import`, `import predict`, `convert`; library `ImportJob` / `ConvertJob` / `ImportSpec` `Schema`): exact spelling, any order; the array order is the cohort's. Refused before any row is read: a field with no column `PULSE_IMPORT_SCHEMA_FIELD_MISSING`; a column no field names `PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED`; a repeated name `PULSE_IMPORT_SCHEMA_DUPLICATE`; an NDJSON / JSON-array key first seen after the first object (whose keys are the columns) `PULSE_IMPORT_SCHEMA_KEY_LATE`. Unknown type name `PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN`; an unknown entry key, an empty array or a schema on a `.pulse` source `PULSE_IMPORT_SCHEMA_INVALID`. `ignore_extra` (`--ignore-extra`) drops unnamed columns and late keys instead. A narrowed output (a projected `--stream`) is never re-imported with its columns silently shifted. Not kept on the sidecar — the cohort header records the schema.
- **Derived columns are not an import job.** To make a window, feature or attribute column a field, `pulse_materialize` it ([`derived-cohorts`](derived-cohorts.md)) — the supported route, exact and atomic; never re-import a streamed result.
- **Inferred floats are `f64`** — decimals stay exact (money sums do not drift). `float: "f32"` (CLI `--float f32`) halves the bytes for a whole import, rounding to ~7 digits.
- Nullability is inferred from the first N rows. A later null promotes the field (`promoted_fields` + `PULSE_IMPORT_NULL_PROMOTED`); an explicit schema raises `PULSE_IMPORT_ROW_ERROR` instead.
- **Widths too.** A value past the sample-inferred width (a 257th parent name in a `categorical_u8`, a larger or non-integer number) promotes the field to the narrowest type that holds it — never a dropped row — with one `width_warnings` entry (`PULSE_IMPORT_WIDTH_PROMOTED`: `field`, `from`, `to`, `source_row`).
- **One casing.** CLI `import <fmt>` / `import predict` / `convert` / `export` / `shard` `--json` `data` uses these snake_case keys too (`rows_imported`, `schema.fields[].type` as a name like `"f64"`).
- **Naive datetimes import as UTC.** A source recorded in local time needs `source_tz` (CLI `--source-tz`; library `ImportJob` / `ImportSpec` `SourceTZ`). DST gap/overlap is FATAL `PULSE_IMPORT_DST_AMBIGUOUS` / `_NONEXISTENT` by default; `dst_policy: earlier|later` resolves and warns `PULSE_IMPORT_DST_RESOLVED` (`zone_warnings`). `Z`/offset literals never move; nor do zoned Arrow / UTC-adjusted Parquet timestamps, while naive ones, INT96 and SPSS datetimes are wall clocks. A handle read in one zone is never reused for another: re-import with `overwrite: true`.
- **Zero rows out of a non-empty source is a FATAL `PULSE_IMPORT_ROW_ERROR`, not an empty import** — details carry `rows_read` / `rows_failed` / `first_row` / `first_error`, and no cohort is written. Export and convert mirror it with `PULSE_EXPORT_ROW_ERROR` (convert's row errors all come from the TARGET writer), and convert writes no `--keep-pulse` intermediate. PARTIAL failure imports the good rows and WARNS `PULSE_IMPORT_ROWS_DROPPED` (`dropped_warnings` here, envelope `warnings` on every CLI import / `convert`; details `dropped`, `imported`, `first_failure {row, column, reason}`, plus `stage` `target`/`keep_pulse` on convert) — fatal under CLI `--strict` (exit 1) or an instance's `Options.Strict`, writing nothing. A source with no data rows stays a legitimate empty cohort.
- **Parent groups: suggest, then apply.** Import with `suggest_groups: true`, read `group_candidates.candidates` (`suggested: true` = the non-overlapping viable set), then re-import with those `{key, members}` as `groups` and `overwrite: true`. Groups write a smaller `0x02` cohort older binaries cannot open; a member varying within its key is FATAL `PULSE_GROUP_MEMBER_NOT_CONSTANT`. Weak groups WARN at the default floor 2 (`PULSE_GROUP_TOO_NARROW` dropped, `PULSE_DEDUP_LOW_RATIO` written) — there is no floor/strict knob because neither changes the bytes. Refused on a `.pulse` passthrough. **No constant elision here** — its value is at-rest bytes and a managed import is a TTL'd cache; use `pulse import <fmt> --elide-constants`.
- **Wide batteries fold into ONE vector** (`vectors`; CLI `--vector` / `--vector-kind`): 2..1024 members vanish, the `vec_f64` / `vec_f32` field takes the first member's position, and requests name it directly in vector slots. All elements empty = null vector, some empty = `NaN`, non-numeric = row error. Bad declarations are FATAL `PULSE_IMPORT_VECTOR_INVALID` before any row (`details.reason`: `dim_out_of_range`, `pattern_no_match`, `column_claimed` by two vectors or a vector and a group, …); SPSS and `.pulse` passthrough are refused. Not profile-gated: a format feature like every field type.
- **A native list column becomes a `vec_f64` / `vec_f32` only when asked** (bare `vectors: ["emb"]`): Arrow FixedSizeList / LIST, Parquet LIST, NDJSON / JSON arrays, read typed (never via the pipe-joined text); dim = the first non-null list's length; no labels. Null list or all-null elements = null vector, a null element = `NaN`. FATAL `PULSE_IMPORT_VECTOR_INVALID` on `list_length_varies`, `element_not_numeric` (a quoted `"3"` too), `not_a_list`, `dim_out_of_range`, `list_dim_unknown` (no non-null list); CSV / TSV / Excel `no_columns`. Undeclared, a list column stays a `set_*` byte-identically.
- **One dispatch.** Detection and reader construction use the `io` factory (`io.FormatFromPath` → `io.NewReader`), the same one the CLI and embedders use. No extension match and no `format` → `PULSE_IMPORT_FORMAT_UNKNOWN`; an explicit `format` outside the list → `PULSE_IO_FORMAT_UNSUPPORTED`.
- A `.pulse.zst` (zstd transfer artifact) is refused with `PULSE_COHORT_COMPRESSED`, even renamed `.pulse`. Decompressing is an operator act: `pulse import transfer` (CLI) — no MCP slot.
- Passthrough skips copy + sidecar (`pulse_drop` no-ops). MCP success rebinds session-scoped tools from the new cohort (as [`tool-inspect`](tool-inspect.md)).

## See

- [`tool-drop`](tool-drop.md), [`tool-imports-list`](tool-imports-list.md), [`cohort-schema-design`](cohort-schema-design.md).
- [`spss-cohorts`](spss-cohorts.md) — type mapping, derived columns, missing-value split, metadata sidecar, per-code prose.
- `pulse_errors_lookup`.
