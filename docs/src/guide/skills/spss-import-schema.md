```yaml
name: spss-import-schema
description: How a .sav / .zsav file imports — the declared dictionary replaces inference, the SPSS-to-Pulse type mapping, the three data encodings, charset and byte-order rules, and the fatal-vs-warning damage diagnostics. Read this when an SPSS import types a column unexpectedly or refuses a file.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, sav, zsav, import, type mapping, charset, byte order, bytecode, diagnostics]
requires: [io_format:spss]
```

# SPSS import — schema and file reading

Part of the SPSS surface; entry skill [`spss-cohorts`](spss-cohorts.md). Missing values: [`spss-missing-values`](spss-missing-values.md); response sets and derived columns: [`spss-response-sets`](spss-response-sets.md).

## Schema-authoritative import

`.sav` declares every column ⇒ `internal/io/spss` implements `io.SchemaAwareReader`, `internal/io/infer.go`'s sample-and-vote pass skipped for `pulse import spss` / `pulse import auto` / `pulse_import` / `pulse convert` alike. Hence:

- Inference-steering slots inert: `SampleRows`, `SetInferenceMinPct`, `SetDelimiters`. `ColumnTypeOverrides` is refused (`PULSE_IMPORT_OVERRIDE_INVALID`) — re-type via an explicit `ImportJob.Schema`.
- **No null promotion** — declared nullability is a contract; an unexpected null is `PULSE_IMPORT_ROW_ERROR`, never a silent widening. `promoted_fields` always empty.
- Explicit `ImportJob.Schema` still wins.

| SPSS | Pulse | Why |
|---|---|---|
| numeric (F/E/COMMA/DOT/PCT…) | `f64` | no range-probe narrowing — a probe types two identical files differently |
| numeric with value labels | `categorical_u8/u16/u32` | width from distinct count; past `u32` → `PULSE_SPSS_CATEGORICAL_OVERFLOW` (hard; dropping values is worse) |
| string (A*) | `categorical_*` | near-unique → `PULSE_SPSS_CARDINALITY_HIGH` (free-text signature), still imports |
| very long string (>255 bytes) | one `categorical_*` | record `7/14` segments it; Pulse rejoins RAW bytes, decodes once |
| DATE/ADATE/EDATE/SDATE/JDATE | `date`, else `datetime` + `PULSE_SPSS_DATE_WIDENED` | widens only on a time-of-day value; pre-1970 stays `date` (signed epoch **days**) |
| DATETIME | `datetime` (epoch **seconds**; naive wall clock, `--source-tz` applies) | fractional-second / non-finite / out-of-int64 demotes to `f64` raw SPSS seconds + `PULSE_SPSS_TEMPORAL_PRECISION` |
| TIME/DTIME | `f64` seconds | durations, never a `datetime` |
| system-missing (sysmis) | null (bitmap bit) | the one missing state with a format sentinel |
| numeric user-missing | null + `<var>_missing` sibling | [`spss-missing-values`](spss-missing-values.md) |
| categorical/string user-missing | dictionary entries, verbatim + FLAGGED | [`spss-missing-values`](spss-missing-values.md) |
| multiple-DICHOTOMY set (`7/5`, `7/7`, `7/19`) | every constituent PLUS a derived `set_*` | additive, never a replacement |
| multiple-CATEGORY set | N `categorical_*`; definition sidecar-only | positional, duplicate-tolerant ⇒ not a set |

**Labels naming only missing codes do NOT make a variable categorical** — `INCOME` labelled solely on 97/98/99 is continuous. Labels code the variable iff one names a value that is neither user-missing nor sysmis.

## Reading real files

**All three data encodings read**, identical cohorts from identical content: uncompressed; **bytecode** (SPSS's save default — bias from the header, not a hardcoded 100); **ZSAV** (zlib blocks inflating to a *bytecode* stream — two layers, not a third encoding; `.zsav` carries `$FL3` not `$FL2`). Pulse never writes ZSAV.

**Text decodes out of the file's charset** — record `7/20` (a NAME), else `7/3` (numeric code), else UTF-8; on disagreement `7/20` wins. Spellings fold (`windows-1252` = `cp1252` = `1252`), never approximately (`1250` ≠ `1252`). Two hard rules: an undecodable byte errors naming variable and value, **never** a U+FFFD substitution; declared widths are BYTE counts, so padding is trimmed on raw bytes BEFORE decoding. `--charset` / `io.SPSSReaderOptions{Charset}` overrides a self-mislabelling file — **not yet on `pulse import auto` or `pulse_import`**.

**Either byte order reads.** The header layout code decides; record `7/3` corroborates only. A contradiction is FATAL — unlike the charset cross-check one field away — because byte order governs every count, offset and double: the wrong reading yields a whole file of plausible wrong numbers.

**Damage has distinct codes because it has distinct fixes.**

- Fatal: `PULSE_SPSS_FILE_EMPTY`, `_DICT_TRUNCATED`, `_DATA_TRUNCATED`, `_DICT_INVALID` (four damage shapes); `_COMPRESSION_INVALID` (a bytecode command landed where it cannot apply — stream lost sync with the dictionary); `_ZSAV_INVALID` (self-inconsistent `ZHEADER`/`ZTRAILER` index, naming the block) / `_ZSAV_BLOCK_CORRUPT`; `_ENDIANNESS_MISMATCH`; `_CHARSET_INVALID` / `_CHARSET_UNSUPPORTED`.
- Warnings, because declining loses nothing: `_CHARSET_MISMATCH`, `_MAGIC_FLAG_MISMATCH`, `_VALUE_LABELS_DROPPED` (unbindable record `3`/`4` set — a label is display metadata, so refusing the file would cost the data to save the labels), `_VERY_LONG_STRING_INVALID` (unusable `7/14`; segments import as the columns the dictionary literally declares).
- No input panics.

**Warnings are load-bearing.** Every non-fatal `PULSE_SPSS_*` diagnostic changes what the cohort MEANS. They ride `ImportReport.SourceWarnings` / `ConvertReport.SourceWarnings` (the `io.SourceWarningEmitter` interface), the `--json` envelope's `warnings` array, and `Warning [CODE]` lines on the text path. `pulse errors lookup CODE` carries the fixup.
