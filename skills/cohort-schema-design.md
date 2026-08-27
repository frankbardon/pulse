---
name: cohort-schema-design
description: Field-type selection for .pulse cohorts — 18 types, nullability via per-record bitmap, shard archive anchor syntax, description-length cap. Use when picking schema types, evaluating storage layout, or interpreting a cohort returned by pulse_inspect.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [u4, u8, u16, u32, u64, f32, f64, decimal128, categorical_u8, categorical_u16, categorical_u32, packed_bool, date, datetime, set_u8, set_u16, set_u32, set_u64]
---

# Cohort schema design

Pick the right `.pulse` field type, decide nullability, address shards. The schema lives in the cohort header; `pulse_inspect` is the surface for reading it.

## Field-type matrix (all 18)

| Name | Bytes | Dict? | Bit-packed? | Notes |
|---|---|---|---|---|
| `u4` | 0 | no | yes | 0..15 |
| `u8` | 1 | no | no | 0..255 |
| `u16` | 2 | no | no | 0..65,535 |
| `u32` | 4 | no | no | 0..~4.29B |
| `u64` | 8 | no | no | |
| `f32` | 4 | no | no | ~7 sig digits; index key = raw bit pattern (`-0.0`/NaN caveat) |
| `f64` | 8 | no | no | ~15 sig digits; same bit-pattern caveat |
| `date` | 4 | no | no | epoch days since 1970-01-01 |
| `datetime` | 8 | no | no | epoch **seconds**, naive UTC; index-key literal parses as a datetime, never a float |
| `packed_bool` | 0 | no | yes | 1 bit |
| `categorical_u8` | 1 | inline, ≤256 | no | dict-encoded; index key = dictionary ID |
| `categorical_u16` | 2 | inline, ≤65,536 | no | |
| `categorical_u32` | 4 | inline, ≤~4.29B | no | |
| `decimal128` | 16 | no | no | per-field `(precision, scale)`; index key = exact mantissa, no float round-trip |
| `set_u8` | 1 | shared, ≤8 labels | no | multi-select bitmask; **not** index-keyable |
| `set_u16` | 2 | shared, ≤16 | no | |
| `set_u32` | 4 | shared, ≤32 | no | |
| `set_u64` | 8 | shared, ≤64 | no | |

`set_*` mask bit `i` = label `dict[i]` selected; empty mask is a valid value (NOT null). Nullability is opt-in per field via the bitmap; all 18 types participate identically.

`date` and `datetime` are NOT interchangeable — days vs. seconds, a factor of 86,400. Both are accepted by `GROUP_DATE` / `GROUP_DATE_RANGES` / `FILTER_DATE_RANGES`, which day-truncate `datetime` to the UTC calendar day. Sub-second timestamps: `u64` microseconds. Resolution, timezone and text-format detail belong to `type-date` / `type-datetime`.

## Selection heuristics

- Counts / IDs: smallest unsigned width that fits the max.
- Floats: `f32` for measurements; `f64` for scores or wide dynamic range.
- Money: `decimal128` — see `financial-cohorts`.
- Booleans: `packed_bool`. Small ordinals (Likert, grades): `u4`.
- Strings: always categorical. Width by distinct cardinality.
- Multi-select: `set_*`. Width by distinct-label cap.
- Sometimes-missing values: pick base type, then `Nullable: true`.

## Nullability + per-record bitmap

When at least one field is `Nullable: true`, every record carries a trailing bitmap of `ceil(field_count / 8)` bytes after the payload:

- Field index `i` → byte `i/8`, bit `i%8` (LSB-first). `1` = null, `0` = present.
- No nullable fields ⇒ no bitmap; zero-overhead path.

The bitmap is the sole null mechanism. No type has an inline sentinel — `decimal128` all-zero bits is decimal zero, not null. Null-skip semantics for sum/mean/percentile are central.

**Inferred imports promote on out-of-sample nulls.** Schema inference reads only the first `--sample-rows` (default 500) rows to decide nullability. A null (`""`/`null`/`na`/`n/a`) past that window promotes the field to nullable and continues rather than failing — emitting `PULSE_IMPORT_NULL_PROMOTED` (also `ImportReport.PromotedFields` / `Result.promoted_fields`). Applies to every inferred text/columnar import (csv, tsv, ndjson, jsonarray, parquet, arrow, excel). An **explicit `--schema`** is a contract: a null in a declared-non-nullable field stays `PULSE_IMPORT_ROW_ERROR`. Avoid surprises: mark sometimes-missing fields `"nullable": true`, or raise `--sample-rows`.

## Bit-packed runs

`u4` and `packed_bool` report `ByteSize() == 0` and share bytes with adjacent packed fields. Place packed fields together for optimal layout. Reordering can change byte offsets even when types are unchanged.

## Width overflow

- `PULSE_IMPORT_CATEGORICAL_OVERFLOW` / `PULSE_IMPORT_CATEGORICAL_UNBOUNDED` — categorical width exceeded or dict unbounded.
- `PULSE_IMPORT_SET_OVERFLOW` — `set_*` cardinality exceeded width.
- `PULSE_SHARD_DICT_WIDTH_OVERFLOW` — shard insert would expand union dict past declared width.

Mitigation: pick widths with growth headroom up front.

## Field descriptions

- Capped at 1000 bytes per field. Over-cap → `PULSE_IMPORT_DESCRIPTION_TOO_LONG`.
- Empty, sub-10-character, or generic ("n/a", "tbd", "unknown", "field", "data", "value", "column") → `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (warning; error under strict).
- Style: concise, third-person, present-tense; state what the field represents, its units, and domain semantics.

## Sharded cohorts

A `.pulse` path resolves to one of two shapes; dispatch on the leading 4 bytes:

1. **Single-file.** Magic `PULSE\x00\x00\x00` + format byte `0x01`. Schema, dicts, records.
2. **Shard archive.** Uncompressed Zip64 (Method 0); magic `PK\x03\x04`. Reserved `_schema.pulse` (header-only canonical schema + `SHRD` trailer with `aggregate_record_count` + `shard_count`) plus N standalone shard payloads.

Old single-file readers fail loud on archive magic.

### Cohesion

- Structural: **strict** at insert. Field count, names, type bytes, byte offsets, bit positions must match canonical. Mismatch → `PULSE_SHARD_SCHEMA_MISMATCH`.
- Descriptions: **tolerant**. Divergence → `PULSE_SHARD_DESCRIPTION_DIVERGENCE` (warning); canonical wins.
- Categorical / set dictionaries: union-merge. Canonical entries first; new entries appended; incoming records byte-rewritten with remapped indices.
- Prefix-only validator raises `PULSE_SHARD_DICT_DIVERGENCE` when embedders coordinate dicts upstream.

### Anchor syntax

```
respondents/Q1.pulse                 → full sharded cohort (union semantics)
respondents/Q1.pulse#20190101.pulse  → named shard, as one-shard cohort
```

Anchor against single-file → `PULSE_ARCHIVE_MAGIC_INVALID`. Missing anchor → `PULSE_SHARD_MISSING`.

### Memory shape

Materializing ops (percentile / median aggs, `ATTR_PERCENTILE`, `GROUP_QUANTILE`, `GROUP_DATE`, windows, decimal paths, tier-1 tests + groupers/features/two-pass attrs, tier-2 post tests) materialize across the **union** of shards. Median-of-medians is not the median. Memory scales with shard count.

### Concurrency

No concurrent-writer protection: two writers race, last wins. Readers snapshot at open. Caller owns single-writer architecture or an advisory lock.

## Sidecar index

`Pulse.Lookup` / `pulse index build` use a **separate file** — `cohort.pulse.<keyhash>.idx`; the `.pulse` layout above stays untouched. Format **v3**:

1. 9-byte header: magic `PULSEIDX` + version `0x03` (own magic/version, distinct from `encoding.MagicBytes`/`FormatVersion`).
2. 32-byte SHA-256 source fingerprint.
3. Key-spec: ordered key columns + field types.
4. `SourceSize` (u64) + `SourceModTime` (i64 Unix ns) — staleness snapshot.
5. `u32 bucket_count`.
6. Fixed-width `bucket_count × u64` offset table — directly addressable, O(1) single-bucket seek.
7. Self-delimited bucket data: FNV-1a hash buckets → `[]uint64` row-id multimap.

A lookup hashes the key, seeks its offset entry, seeks that bucket's data, then seeks to each matched record via `RecordLocator` — never a full-cohort or full-index read. Read-path staleness is an O(1) size+mtime stat (mismatch → `PULSE_INDEX_STALE`); `pulse index verify` recomputes the full SHA-256 instead.

**Keyable types** (`processing.IsIndexKeyableFieldType`): every type in the matrix above EXCEPT `set_*` — a multi-select mask has no single unambiguous equality value, so use `FILTER_SET` instead. Per-type equality caveats live in that matrix's Notes column, not here.

**Constraints:** single-file cohorts only (`archive.pulse#shard.pulse` anchor is a tested single-shard workaround); equality-only, full-key required, composite-key order significant end to end. The `PULSE_INDEX_*` / `PULSE_LOOKUP_*` error set and its fixups are `tool-lookup`'s surface.

## SPSS import (`.sav` / `.zsav`)

An SPSS system file is the one source whose schema Pulse does **not** infer. Its dictionary DECLARES every column, so `io/spss` implements `io.SchemaAwareReader` and the whole sample-and-vote pass in `io/infer.go` is skipped — for `pulse import spss`, `pulse import auto`, `pulse_import` and `pulse convert` alike. Consequences: the inference-steering slots (`SampleRows`, `SetInferenceMinPct`, `SetDelimiters`, `ColumnTypeOverrides`) are inert, and there is no null promotion — declared nullability is a contract, so an unexpected null is a `PULSE_IMPORT_ROW_ERROR`, never a silent widening. An explicit `ImportJob.Schema` still wins outright.

| SPSS | Pulse | Why |
|---|---|---|
| numeric (F/E/COMMA/DOT/PCT…) | `f64` | No integer narrowing by range probe. A probe would type two otherwise identical files differently. |
| numeric with value labels | `categorical_u8/u16/u32` | Width from the distinct count; overflow past `u32` → `PULSE_SPSS_CATEGORICAL_OVERFLOW` (hard error — dropping values is worse). |
| string (A*) | `categorical_*` | Near-unique columns warn `PULSE_SPSS_CARDINALITY_HIGH` (free-text signature) but still import. |
| very long string (>255 bytes) | one `categorical_*` column | Record `7/14` segments it across several physical variables; Pulse reassembles them into one logical column. |
| DATE/ADATE/EDATE/SDATE/JDATE | `date`, or `datetime` on `PULSE_SPSS_DATE_WIDENED` | Widens when a value carries a time of day or predates 1970 — `date` is an unsigned epoch **day**. |
| DATETIME/TIME/DTIME | `datetime` (epoch **seconds**) | A fractional second / non-finite / out-of-int64 value demotes the column to `f64` raw SPSS seconds with `PULSE_SPSS_TEMPORAL_PRECISION`. |
| system-missing (sysmis) | null (bitmap bit) | The one missing state the format has a sentinel for. |
| user-missing values | ordinary data, kept verbatim | The null bitmap records *that* a value is missing, never *why*, so collapsing a user-missing code to null would destroy the reason. The reason-preserving `<var>_missing` sibling is not built yet — today the codes simply survive as values. |

**Dictionaries hold SPSS CODES, not labels.** A `categorical_*` dictionary for a labelled variable contains `"1"`, `"2"`, … — the source's own numeric codes, in the source's own order, because entry order IS the on-wire encoding. Two SPSS codes may legitimately share one value label, so a label-keyed dictionary would collapse them and destroy the code. Analysts get labels at output time through a `LabelTable` (`label-display`), never from the cohort itself. A cell whose text is a null sentinel (`""`, `NA`, `N/A`, `NULL`) imports as null and warns `PULSE_SPSS_NULL_TOKEN_COLLISION`.

**Very long strings reassemble; the segments never surface as columns.** A string wider than 255 bytes cannot state its width in the record type 2 `type` field, so SPSS splits ONE logical variable across SEVERAL physical variables and records the join in record `7/14`. That is a SECOND segmentation stacked on the ordinary 8-byte element cut every >8-byte string already uses — a 600-byte value is 3 physical variables (255 + 255 + 96 declared) over 76 elements. **A non-final segment declares 255 bytes but carries 252**; the last 3 are unused, which is why the stride is 252 and why a 256-byte string is two segments declaring 255 + 4 (summing to 259, three more than the variable holds). Pulse joins the RAW bytes and decodes once — decoding per segment would cut any multi-byte character straddling the boundary. `ReadHeader` shows the one logical column; `columnMapping.declaredWidth` is the LOGICAL total (600), with the per-segment layout retained beside it for the write path. A `7/14` record that cannot be applied (unknown variable, impossible width, segment widths that disagree with the declared total) is `PULSE_SPSS_VERY_LONG_STRING_INVALID` — a **warning**, because declining to join loses nothing: the segments import as the separate columns the dictionary literally declares. Records `7/21` (long string value labels) and `7/22` (long string missing values) decorate any string over 8 bytes, very long or not; both name the variable by its record `7/13` LONG name in practice, and Pulse falls back to the short name.

**Warnings are load-bearing.** The `PULSE_SPSS_*` diagnostics above are non-fatal but they change what the cohort MEANS. They ride `ImportReport.SourceWarnings` / `ConvertReport.SourceWarnings` (via the `io.SourceWarningEmitter` optional interface) and surface on the `--json` envelope's `warnings` array and as `Warning [CODE]` lines on the text path. `pulse errors lookup CODE` carries the per-code fixup.

**Data-section encodings — all three read.** Uncompressed, **bytecode compression** (SPSS's own save default) and **ZSAV** (zlib blocks, what a `.zsav` carries) all decode, and produce identical cohorts from the same logical content. Bytecode is a stream of 8-command blocks, each followed by its 8-byte payloads: `0` pad, `1..251` the integer `command - bias` (the bias comes from the **header**, not a hardcoded 100), `252` end, `253` a verbatim value, `254` an all-spaces string segment, `255` sysmis. A command that cannot apply to the element position it landed on (spaces into a numeric, sysmis into a string) means the stream lost sync with the dictionary → `PULSE_SPSS_COMPRESSION_INVALID`; a stream cut mid-case or mid-payload → `PULSE_SPSS_DATA_TRUNCATED`.

**ZSAV is two layers, not a third encoding.** The zlib blocks inflate to a *bytecode command stream*, which is then decoded exactly as above — they do not hold case data. A `.zsav` also carries the `$FL3` header magic instead of `$FL2`. The blocks are indexed by a `ZHEADER` (its own offset, the trailer's offset, the trailer's length) plus a `ZTRAILER` carrying one entry per block: offset and size in BOTH the compressed and the uncompressed coordinate spaces. Those entries must tile the compressed region exactly, and Pulse validates them before inflating anything — an index that disagrees with itself is `PULSE_SPSS_ZSAV_INVALID` **naming the block** (1-based, also in `Details["block"]`); a block that fails to inflate, or inflates short or long, is `PULSE_SPSS_ZSAV_BLOCK_CORRUPT`. Read-only by design: Pulse never writes ZSAV.

**Text is decoded out of the file's charset.** A pre-Unicode `.sav` holds codepage bytes, not UTF-8. The charset comes from record `7/20` (a NAME), else record `7/3` (a numeric character code), else UTF-8; spellings fold (`windows-1252` = `cp1252` = `1252`) but never approximately (`1250` ≠ `1252`). When both records are present and disagree the `7/20` name WINS and `PULSE_SPSS_CHARSET_MISMATCH` warns — the name is the more expressive statement (code `3` covers ISO-8859-1, windows-1252 and more alike) and writers leave the numeric field stale; codes `2`/`3` (ASCII) are never a disagreement. Two hard rules: an undecodable byte is `PULSE_SPSS_CHARSET_INVALID` naming the variable and the value, NEVER a U+FFFD substitution (a replacement character is indistinguishable from data downstream); and declared **widths are BYTE counts**, so a value is trimmed of its `0x20` padding on the raw bytes BEFORE decoding, and `columnMapping.declaredWidth` retains the source byte width for the write path. A charset with no decoder — unregistered, registered-but-unimplemented (EBCDIC), or not an ASCII superset (UTF-16) — is `PULSE_SPSS_CHARSET_UNSUPPORTED`, never a silent fall back to UTF-8. `spss.WithCharset(name)` / `--charset` / `format.ReaderOptions.Charset` overrides a file that mislabels itself and changes decoding only; the file's own declaration is still retained. The CLI flag is on `import spss`, `import predict`, `import schema-template`, `convert` and `convert predict`; **`pulse import auto` and the `pulse_import` MCP tool have no override yet.** The case that most needs it is a file declaring NEITHER `7/20` nor `7/3`: the UTF-8 default is strict, so its first 8-bit byte fails and no evidence in the file can settle it.

**Either byte order reads, and a contradiction is fatal.** The header layout code decides: it always holds `2` or `3`, and neither byte-swaps into anything in range, so reading it both ways is unambiguous. Record `7/3` states the order a SECOND time (`1` big, `2` little) and is a corroboration only — reading it already needs the order. A clean contradiction is `PULSE_SPSS_ENDIANNESS_MISMATCH`, a HARD error, unlike the charset cross-check one field away: byte order governs every count, offset and double in the file, so the wrong reading yields a whole file of plausible wrong numbers rather than one bad field. Not contradictions: no `7/3` at all, endianness `0`, or any value outside `{1,2}` (that warns `PULSE_SPSS_EXTENSION_INVALID` and is ignored). The magic (`$FL2`/`$FL3`) and the compression flag are also cross-checked, but that one WARNS (`PULSE_SPSS_MAGIC_FLAG_MISMATCH`) and the flag wins — the flag describes the bytes, the magic is a stale-able generation label.

**Damaged files degrade rather than break, and say which damage.** Four distinct codes, because they have four distinct fixes: `PULSE_SPSS_FILE_EMPTY` (zero bytes — a target created and never written), `PULSE_SPSS_DICT_TRUNCATED` (stops mid-dictionary), `PULSE_SPSS_DATA_TRUNCATED` (stops mid-case), `PULSE_SPSS_DICT_INVALID` (structurally wrong — bad magic, unidentifiable byte order, unknown record tag, out-of-range field). No input panics; every failure is a coded error. A record `3`/`4` value-label set that names variables it cannot bind to — mixed type/width, a string wider than the 8-byte value slot (those belong in `7/21`), an index landing on a string continuation — is DROPPED with `PULSE_SPSS_VALUE_LABELS_DROPPED` naming the variable and the file imports: a label is display metadata, so refusing the file costs the data to save the labels, and binding it anyway would mislabel silently. Corrupt indices (below `1`, past the dictionary) stay fatal `PULSE_SPSS_DICT_INVALID` — those are damage, not dialect.

**Read-only.** There is no SPSS writer: an SPSS *output* target returns `PULSE_SPSS_EXPORT_UNSUPPORTED`, deliberately distinct from an unknown-format error because the extension IS recognised. `pulse convert survey.sav out.csv` works; `pulse convert data.csv out.sav` does not.

## Cross-links

- `financial-cohorts` — `decimal128` rules.
- `response-components` — `data.components.run.shard_count` + `partial_cohort_reason`.
- `aggregation-guide` / `grouper-design` / `attribute-composition` — `set_*` operator surfaces.
- `tool-lookup` — point-lookup MCP surface built on this sidecar format.
- `tool-import` — the import MCP surface, incl. the SPSS format enum.
- `label-display` — resolving SPSS value labels from the numeric codes the cohort stores.
