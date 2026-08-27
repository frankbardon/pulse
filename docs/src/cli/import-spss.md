# pulse import spss

**Audience:** CLI users bringing an IBM SPSS Statistics system file
(`.sav`, `.zsav`) into a `.pulse` cohort. Defined in
[`internal/cli/import.go`](https://github.com/frankbardon/pulse/blob/main/internal/cli/import.go);
the adapter itself is
[`io/spss/`](https://github.com/frankbardon/pulse/tree/main/io/spss).

SPSS is the one import format Pulse does **not** guess at. Every other
source (CSV, NDJSON, Parquet, …) is sampled and voted on by
`io/infer.go`; a `.sav` file carries a dictionary that *declares* every
variable's type, its missing-value rules and its value labels, so the
adapter implements `io.SchemaAwareReader` and hands that dictionary
straight to the encoder. Inference never runs.

> **Read-only.** There is no `pulse export spss` and no SPSS writer.
> An SPSS *output* target returns `PULSE_SPSS_EXPORT_UNSUPPORTED`.

> **Uncompressed files only, today.** Bytecode compression is SPSS's own
> save default and `.zsav` is always compressed, so most files taken
> straight from SPSS fail with `PULSE_SPSS_COMPRESSION_UNSUPPORTED`. See
> [Compression](#compression) below.

## Synopsis

```
pulse import spss --input PATH --output PATH.pulse [--schema FILE] [--json]
pulse import predict --input PATH [--format spss] [--json]
pulse import auto PATH [--handle NAME] [--ttl DUR] [--json]
pulse convert PATH.sav OUT.csv
```

`.sav` and `.zsav` both resolve to the same `spss` format identifier.
`import predict`, `import auto` and `convert` detect it from the
extension, so `--format` is optional on all three; only the explicit
`pulse import spss` leaf names the format positionally.

## A worked import

```bash
pulse import spss --input survey.sav --output survey.pulse
```

```
Imported 2 rows to survey.pulse
```

With `--json` the same run emits the standard envelope, carrying the
schema the SPSS dictionary declared:

```json
{
  "format_version": "1.1",
  "data": {
    "RowsImported": 2,
    "Schema": {
      "Fields": [
        {"Name": "ID",   "Type": 5, "Nullable": false, "CsvColumnIdx": 0, "Description": ""},
        {"Name": "SEX",  "Type": 9, "Nullable": true,  "CsvColumnIdx": 1, "Description": "Sex"},
        {"Name": "NAME", "Type": 9, "Nullable": false, "CsvColumnIdx": 2, "Description": ""}
      ]
    },
    "RowErrors": null,
    "PromotedFields": null,
    "SourceWarnings": null
  },
  "errors": [],
  "warnings": []
}
```

`Description` is lifted from the SPSS variable label. `PromotedFields`
is **always** `null` for an SPSS import — see
[Flags](#flags-and-what-spss-ignores).

## Type mapping

| SPSS | Pulse | Notes |
|---|---|---|
| numeric (`F`, `E`, `COMMA`, `DOT`, `PCT`, …) | `f64` | No integer narrowing by range probe — a probe would type two otherwise identical files differently |
| numeric with value labels | `categorical_u8` / `u16` / `u32` | Width from the distinct code count; past `u32` → `PULSE_SPSS_CATEGORICAL_OVERFLOW` |
| string (`A*`) | `categorical_*` | Near-unique columns warn `PULSE_SPSS_CARDINALITY_HIGH` and still import |
| `DATE` / `ADATE` / `EDATE` / `SDATE` / `JDATE` | `date`, or `datetime` with `PULSE_SPSS_DATE_WIDENED` | Widens when a value carries a time of day or predates 1970 |
| `DATETIME` / `TIME` / `DTIME` | `datetime` (epoch seconds) | A fractional-second / non-finite / out-of-`int64` value demotes the column to `f64` raw SPSS seconds with `PULSE_SPSS_TEMPORAL_PRECISION` |
| system-missing (sysmis) | null (bitmap bit) | The one missing state the format has a sentinel for |
| user-missing values | ordinary data, kept verbatim | The null bitmap records *that* a value is missing, never *why* — collapsing a user-missing code to null would destroy the reason |

## Value labels: the cohort stores codes

A labelled SPSS variable becomes a `categorical_*` column whose
dictionary holds the **numeric codes**, not the labels:

```bash
pulse cohort inspect survey.pulse --json
```

```json
{
  "name": "SEX",
  "type": "categorical_u8",
  "description": "Sex",
  "categorical": true,
  "dictionary": { "total_entries": 2, "values": ["1", "2"] }
}
```

`"1"` / `"2"` — not `"Male"` / `"Female"`. This is deliberate. SPSS
permits two distinct codes to share one value label, so a label-keyed
dictionary would silently collapse them and the original code could not
be recovered. Dictionary entry order also *is* the on-wire encoding, so
preserving the source's own code order is what preserves the round trip.

To see labels in output, register a **LabelTable** mapping code → label
(`pulse.Options.LabelTables`, or a directory of JSON files pointed at by
`PULSE_LABEL_TABLES_DIR`) and bind it per request. Labels are an
output-time projection, never a property of the stored cohort; the
`label-display` skill is the full surface.

## Flags and what SPSS ignores

`pulse import spss` takes the shared import flags — it adds none of its
own.

| Flag | Alias | Effect on an SPSS import |
|---|---|---|
| `--input` | `-i` | required — the `.sav` / `.zsav` path |
| `--output` | `-o` | required — the `.pulse` path to write |
| `--schema` | | **wins outright.** An explicit schema file overrides the SPSS dictionary and the adapter's `PulseSchema()` is never called |
| `--sample-rows` | | **inert.** Nothing is sampled |
| `--json` | | standard envelope |

The same inertness applies to the library and managed-import knobs that
exist only to steer inference — `ImportJob.SampleRows`,
`SetInferenceMinPct`, `SetDelimiters` and the `force_type` column
overrides on `pulse import auto`. Forcing a type onto a
dictionary-carrying column would discard the source's category IDs and
rebuild them in first-seen order, which is exactly what an authoritative
schema exists to prevent.

There is also **no null promotion**. For inferred formats a null found
past the sample window widens the field to nullable and reports it in
`promoted_fields`. A declared schema is a contract instead: a null in a
column SPSS declares non-nullable stays a `PULSE_IMPORT_ROW_ERROR`.

## Diagnostics

Non-fatal parse findings are coded `PULSE_SPSS_*` warnings. They do not
stop the import, but they change what the resulting cohort *means*, so
read them. On the text path they print one per line:

```
Warning [PULSE_SPSS_CARDINALITY_HIGH]: spss: variable "COMMENT": the variable has
150 distinct value(s) across 150 case(s), which maps to a categorical_u8 dictionary
of one entry per 1.0 case(s); a near-unique categorical is the free-text signature
and its inline dictionary block is read on every open [record type 2 at byte offset 208]
```

With `--json` they are lifted onto the envelope's `warnings` array —
not buried inside `data`, so a generic envelope consumer sees them:

```json
{
  "code": "PULSE_SPSS_CARDINALITY_HIGH",
  "message": "spss: variable \"COMMENT\": the variable has 150 distinct value(s) ...",
  "details": {
    "variable": "COMMENT",
    "distinct": 150,
    "actual": 150,
    "record_type": "2",
    "offset": 208
  }
}
```

`pulse errors lookup CODE` carries the canonical prose and fixups for
each.

| Code | Raised when |
|---|---|
| `PULSE_SPSS_CARDINALITY_HIGH` | A string column is near-unique — a free-text signature |
| `PULSE_SPSS_DATE_WIDENED` | A date column widened to `datetime` |
| `PULSE_SPSS_TEMPORAL_PRECISION` | A temporal column demoted to `f64` raw seconds |
| `PULSE_SPSS_VALUE_COLLISION` | Two distinct SPSS values resolve to one dictionary entry (reachable cause: the shared import path trims cells, so `" X"` and `"X"` merge) |
| `PULSE_SPSS_MEASURE_LEVEL_MISMATCH` | A `scale`-level variable carries value labels, so it mapped to a categorical whose smart defaults are `AGG_FREQUENCY` / `GROUP_CATEGORY` rather than `AGG_SUM` / `GROUP_RANGE` |
| `PULSE_SPSS_NULL_TOKEN_COLLISION` | A cell's text is a null sentinel (`""`, `NA`, `N/A`, `NULL`) and imports as null |
| `PULSE_SPSS_EXTENSION_UNKNOWN` | A record type 7 extension subtype this reader does not interpret; its bytes are retained verbatim |
| `PULSE_SPSS_EXTENSION_INVALID` | An interpreted subtype carried a payload of the wrong shape; framing stayed sound, only the interpretation is dropped |
| `PULSE_SPSS_DATA_CASE_COUNT_MISMATCH` | The header's declared case count disagrees with the cases present |

## Compression

```bash
pulse import spss --input survey.sav --output survey.pulse
```

```
error: reading authoritative source schema: PULSE_SPSS_COMPRESSION_UNSUPPORTED:
spss: data section: the file uses SPSS bytecode compression (header compression
flag 1), which this reader cannot yet decode; only the uncompressed encoding is
read today [at byte offset 372 (0x174)]
```

Only the uncompressed data section is decoded today. Reading compressed
bytes as though they were uncompressed would yield plausible-looking
garbage, so the import stops instead.

Workaround — re-save the file without compression, then import the copy:

```
SAVE OUTFILE='plain.sav' /UNCOMPRESSED.
```

(In the SPSS GUI: File > Save As with the compression option cleared.)

## Fatal errors

| Code | Meaning |
|---|---|
| `PULSE_SPSS_DICT_INVALID` | The dictionary is malformed |
| `PULSE_SPSS_DICT_TRUNCATED` | The file ends mid-dictionary |
| `PULSE_SPSS_COMPRESSION_UNSUPPORTED` | Bytecode / ZSAV compression — see above |
| `PULSE_SPSS_DATA_TRUNCATED` | The data section ends mid-case |
| `PULSE_SPSS_CATEGORICAL_OVERFLOW` | A labelled variable has more distinct codes than `categorical_u32` holds |
| `PULSE_SPSS_EXPORT_UNSUPPORTED` | An SPSS output target was requested — Pulse cannot write `.sav` |

Parse-stage diagnostics (dictionary walk, data pass) carry `record_type`
and `offset` details pinpointing where in the file they were raised;
schema-mapping diagnostics carry `variable`.

> On the `--json` path a **fatal** import error is currently reported
> under the generic envelope code `IMPORT_ERROR` (`CLI_ERROR` for a
> convert), with the `PULSE_SPSS_*` code carried inside the `message`
> string. Non-fatal `PULSE_SPSS_*` warnings do reach `warnings[].code`
> structurally.

## Converting out

`pulse convert` reads `.sav` and writes any format Pulse can write:

```bash
pulse convert survey.sav survey.csv
```

```
Converted 2 rows: survey.sav -> survey.csv
```

The reverse is recognised and refused, deliberately — the extension *is*
known, so a generic "unsupported format" would be misleading:

```bash
pulse convert data.csv out.sav
```

```
error: PULSE_SPSS_EXPORT_UNSUPPORTED: SPSS (.sav / .zsav) is an import-only
format; Pulse cannot write it yet
```

## Related

- [`pulse cohort inspect`](cohort-inspect.md) — read the schema and dictionaries the import produced
- [`pulse manifest`](manifest.md) — `import.formats[]` declares `spss` with `schema_source: "authoritative"` and `export: false`
- [Adding an I/O Format](../internals/adding-io-format.md) — the `SchemaAwareReader` / `SourceWarningEmitter` contracts this adapter implements
- `skills/cohort-schema-design.md` — the full SPSS mapping table and `.pulse` field-type matrix
- `skills/tool-import.md` — the `pulse_import` MCP surface
