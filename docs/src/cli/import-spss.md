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

> **Codepages decode.** Text is transcoded out of the charset the file
> declares (record `7/20` / `7/3`) into UTF-8, and a byte that charset
> cannot decode is a coded error rather than a `?`. See
> [Character encoding](#character-encoding).

> **`.zsav` imports directly.** All three data-section encodings read:
> uncompressed, bytecode (SPSS's own save default) and ZSAV zlib blocks.
> Nothing needs to be re-saved or converted first. See
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
| very long string (wider than 255 bytes) | one `categorical_*` column | Reassembled from the record `7/14` segments — see [Very long strings](#very-long-strings) |
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
| `PULSE_SPSS_VERY_LONG_STRING_INVALID` | A record `7/14` segmentation could not be reassembled; the segments import as separate columns — see [Very long strings](#very-long-strings) |
| `PULSE_SPSS_DATA_CASE_COUNT_MISMATCH` | The header's declared case count disagrees with the cases present |
| `PULSE_SPSS_CHARSET_MISMATCH` | The file states its character encoding twice and the two disagree — see [Character encoding](#character-encoding) |

## Character encoding

Pre-Unicode `.sav` files hold text in a codepage — windows-1252,
ISO-8859-1, Shift_JIS and the rest — not in UTF-8. Pulse decodes every
string it reads into UTF-8, so a French or German survey saved in 2004
imports as `Zürich` and `Männlich`, not as `Z?rich` and `M?nnlich`.

Decoding is done with [`golang.org/x/text`](https://pkg.go.dev/golang.org/x/text/encoding),
which this feature promoted from an indirect to a **direct** module
dependency — it was already in the graph via the Arrow and Excel
adapters. No cgo, so the `CGO_ENABLED=0` build is unaffected.

**Where the charset comes from**, highest precedence first:

1. the record `7/20` character-encoding **name** (`windows-1252`,
   `UTF-8`, …), which is what PSPP and modern SPSS write;
2. the record `7/3` **character code** — the legacy numeric field
   (`2`/`3` ASCII, `1252`, `65001`, …);
3. UTF-8, for a file that declares neither.

Spelling is forgiving on purpose: `windows-1252`, `Windows_1252`,
`cp1252`, `CP-1252` and `1252` are the same request. It is not *loose* —
`1250` never resolves to windows-1252 no matter how it is punctuated.

**When both are present and they disagree, the `7/20` name wins** and
`PULSE_SPSS_CHARSET_MISMATCH` is raised as a warning. The name is
strictly the more informative statement — code `3` ("8-bit ASCII") is
what a writer emits for ISO-8859-1, windows-1252 and half a dozen
national codepages alike — and writers routinely leave the numeric field
at an ASCII default while filling in `7/20` correctly. A `7/3` code of
`2` or `3` is therefore never treated as a disagreement.

**A byte the declared charset cannot decode is an error, never a
replacement character.** The usual behaviour of a text decoder is to
substitute U+FFFD and carry on, which would fill a cohort with
replacement characters no later stage could tell from data:

```
error: PULSE_SPSS_CHARSET_INVALID: spss: a data value of variable "CITY":
byte 0x81 at position 1 of the value "Z\x81rich" is not decodable in the
declared character encoding windows-1252
```

Almost always the file is wrong about itself rather than the bytes being
wrong — a dictionary transcoded by one tool and re-saved by another keeps
its old `7/20` name. Only the caller can say which is right, so the
library reader takes an override:

```go
r := spss.NewReader(fs, "survey.sav", spss.WithCharset("windows-1252"))
```

It changes **decoding only**; the file's own declaration is still
retained, so a future export re-encodes into what the source said.

> There is no CLI charset override yet — `pulse import spss` always uses
> the file's declaration. Use the library reader when you need one.

**A charset with no decoder is refused** rather than read as UTF-8:
`PULSE_SPSS_CHARSET_UNSUPPORTED` names it. That covers an unregistered
name, a registered one with no implementation (EBCDIC codepages), and
any encoding that is not an ASCII superset — UTF-16 among them, because
a `.sav` pads its fields with the byte `0x20` and delimits its record
`7/5` and `7/13` payloads with ASCII, so an encoding that does not write
ASCII as itself cannot express the format at all.

## Very long strings

SPSS cannot state a string width above 255 in a variable record: the
`type` field is one byte's worth of range. A wider string is therefore
stored as **several physical variables**, and a record `7/14` extension
says how to put them back together.

That is a *second* segmentation, stacked on one that is already there.
Every string over 8 bytes is spread across 8-byte data elements
(continuation records); a very long string is spread across physical
*variables*, each of which is itself spread across elements. A 600-byte
string is three physical variables of 255, 255 and 96 declared bytes,
occupying 32 + 32 + 12 = 76 elements.

The number that decides everything is **252, not 255**. A non-final
segment *declares* 255 bytes but only its first 252 carry the value; the
remaining three are unused. The clearest proof is a 256-byte string: it
is two segments declaring 255 and 4, whose declared widths sum to 259 —
three more bytes than the variable can hold. Only a 252-byte stride
reproduces a 256-byte value.

Pulse folds all of that away. `pulse inspect` and the imported cohort
show **one column**, under the variable's own name; the generated
segment names (`COMMENT0`, `COMMENT1`, …) never appear:

```console
$ pulse import spss -i survey.sav -o survey.pulse
Imported 2 rows to survey.pulse

$ pulse cohort inspect survey.pulse
Fields: 2
  ID                             f64                  Numeric field: ID
  Comments                       categorical_u8       Free text
    dictionary: 2 entries
```

Two properties are worth knowing:

- **Bytes are joined before they are decoded.** A segment boundary falls
  at a fixed byte offset that knows nothing about characters, so a
  multi-byte character can straddle it. Pulse concatenates the raw bytes
  first and runs the charset decoder once over the result. Decoding each
  segment separately would cut such a character in half.
- **The layout is kept, not discarded.** The number of physical
  variables, their names and their declared widths survive the fold, so
  an export can re-segment the value the way the source had it. The
  retained *width* is the logical total (600), not the 255 any one
  segment declares.

A record `7/14` that cannot be applied is
`PULSE_SPSS_VERY_LONG_STRING_INVALID`, and it is a **warning**. The
record only says how to *join* columns that are already in the file, so
declining to join loses no bytes at all: the segments import as the
separate columns the dictionary literally declares, under their own
names, and the warning says which variable and why.

Two sibling records decorate wide strings, and both apply to *any*
string over 8 bytes — not only very long ones, because the 8-byte value
slot in a variable record is what they exist to get around:

| Record | Carries | Where it lands |
|---|---|---|
| `7/21` | value labels for a wide string | The column's `categorical_*` dictionary, in record order, exactly as records `3`/`4` do for narrow ones |
| `7/22` | up to three missing values for a wide string | The variable's missing-value specification. The slot is fixed at 8 bytes because SPSS itself compares only a long string's first 8 |

Both name their variable by its **long** name (record `7/13`) in every
file the wider ecosystem will read; Pulse falls back to the short name
for writers that used it.

> **Cross-check note.** R's `foreign` reads `7/14` files but deliberately
> does *not* reassemble — it imports each segment as its own variable and
> says so in a warning. R's `haven` (ReadStat) does reassemble, but
> concatenates each non-final segment's full **255** declared bytes
> rather than its 252 content bytes, so it returns a 600-byte value as
> 606 bytes with three spurious spaces at every segment boundary, and a
> 256-byte value as 259. The divergence is invisible for the common case
> — a wide field holding a short value, where trailing-space trimming
> hides it — and only appears once a value actually exceeds 252 bytes.

## Compression

A `.sav` data section arrives in one of three encodings, and the file
header says which. All three import.

| Encoding | Header flag | Header magic | Status |
|---|---|---|---|
| Uncompressed | 0 | `$FL2` | Read |
| Bytecode | 1 | `$FL2` | Read — **this is what SPSS writes by default** |
| ZSAV (zlib blocks) | 2 | `$FL3` | Read — **this is what a `.zsav` carries** |

Nothing needs to be passed to select one. The flag is read from the
header and the right decoder runs:

```bash
pulse import spss --input survey.sav --output survey.pulse
```

A compressed and an uncompressed copy of the same data produce
identical cohorts, whichever of the three encodings was used.

### How bytecode compression works

The data section becomes a stream of blocks. Each block is **eight
command bytes** followed immediately by the eight-byte payloads that
those commands asked for:

| Command | Means |
|---|---|
| `0` | Padding — occupies a command slot, produces no value. Fills out the final block. |
| `1..251` | The whole number `command - bias`. The bias is read from the header; it is conventionally 100, giving the range −99…151. |
| `252` | End of the data section. |
| `253` | The next eight bytes of the stream are the value, verbatim. |
| `254` | An all-spaces eight-byte string segment. |
| `255` | System-missing. |

The saving comes from survey data being mostly small whole numbers: one
byte instead of eight. It is lossless — anything the commands cannot
express falls through to `253` unchanged.

### When a compressed file will not read

```
error: reading authoritative source schema: PULSE_SPSS_COMPRESSION_INVALID:
spss: data section: the compressed stream asks for an all-spaces string
segment (command 254) at element 1 of a case, where the dictionary declares
a numeric element; the stream has lost sync with the dictionary
[at byte offset 372 (0x174)]
```

`PULSE_SPSS_COMPRESSION_INVALID` means the command stream and the
dictionary disagree — a command landed on an element position it cannot
apply to. Every element after that point would be read against the wrong
variable, so the import stops rather than emitting plausible numbers.
Re-export the file from SPSS or PSPP; a desynchronised stream cannot be
repaired by hand.

A stream cut short — mid-case, or with a `253` whose eight bytes never
arrived — is `PULSE_SPSS_DATA_TRUNCATED` instead.

### How ZSAV works

ZSAV is **two layers, not a third encoding**. The zlib blocks do not
hold case data — they inflate to a *bytecode command stream*, exactly the
one described above, which is then decoded exactly as it would be in a
plain `.sav`. A reader that treated the inflated bytes as values would
produce plausible numbers from every file, which is why the layering is
spelled out rather than assumed.

The blocks are described by an index at both ends of the data section:

| Structure | Where | Carries |
|---|---|---|
| `ZHEADER` | first 24 bytes of the data section | its own offset, the trailer's offset, the trailer's length |
| compressed blocks | after the `ZHEADER` | one independent zlib stream each |
| `ZTRAILER` | end of the file | the bias (negated), a reserved zero, the uncompressed block size, the block count, then one 24-byte entry per block |

Each entry gives its block's offset **and** size in two coordinate
spaces — where the block actually sits in the file, and where it would
sit if the file were not compressed. That redundancy is the point: the
entries must tile the compressed region exactly, each block starting
where the previous one ended, and Pulse checks every one of them
*before* inflating anything. Inflating from an offset no writer ever
wrote a stream at either fails or, worse, succeeds on something.

### When a `.zsav` will not read

```
error: reading authoritative source schema: PULSE_SPSS_ZSAV_INVALID:
spss: data section: ZSAV block 3 of 40 declares compressed offset 1857,
but the block before it ends at 1856; the compressed offsets must run on
without a gap [at byte offset 4218 (0x107A)]
```

`PULSE_SPSS_ZSAV_INVALID` means the block index does not describe the
file it sits in. The message **names the block**, and the same 1-based
number is carried structurally under `details.block`, so a fault in one
block of a thousand is actionable rather than a shrug. Re-export the
file; a block index cannot be repaired by hand.

`PULSE_SPSS_ZSAV_BLOCK_CORRUPT` is the other half: the index was
coherent, the offsets were right, and the bytes at them are damaged — a
block that will not inflate, fails its zlib checksum, or inflates to a
size other than the one its entry declares. A short or long block is as
fatal as one that fails outright, because the blocks concatenate into a
single command stream and a wrong-length block shifts every later value
onto the wrong variable. That one is usually a truncated download:
compare the file's byte length against the source.

A `.zsav` whose *inflated* stream disagrees with the dictionary raises
the bytecode codes above (`PULSE_SPSS_COMPRESSION_INVALID`,
`PULSE_SPSS_DATA_TRUNCATED`), not a ZSAV code — the zlib layer was
intact, so pointing at it would send you to the wrong place.

> **Read-only by design.** Pulse never writes ZSAV, and there is no
> `pulse export spss` at all.

## Fatal errors

| Code | Meaning |
|---|---|
| `PULSE_SPSS_DICT_INVALID` | The dictionary is malformed |
| `PULSE_SPSS_DICT_TRUNCATED` | The file ends mid-dictionary |
| `PULSE_SPSS_COMPRESSION_UNSUPPORTED` | A compression flag the format does not define — all three defined encodings are read |
| `PULSE_SPSS_COMPRESSION_INVALID` | A bytecode stream that disagrees with its own dictionary, or an unusable compression bias — see above |
| `PULSE_SPSS_ZSAV_INVALID` | A ZSAV block index that does not describe its file — names the block — see above |
| `PULSE_SPSS_ZSAV_BLOCK_CORRUPT` | A ZSAV zlib block that will not inflate, or inflates to the wrong length — names the block — see above |
| `PULSE_SPSS_DATA_TRUNCATED` | The data section ends mid-case, or a `253` command's value is missing |
| `PULSE_SPSS_CATEGORICAL_OVERFLOW` | A labelled variable has more distinct codes than `categorical_u32` holds |
| `PULSE_SPSS_CHARSET_UNSUPPORTED` | The declared character encoding resolves to no decoder — see [Character encoding](#character-encoding) |
| `PULSE_SPSS_CHARSET_INVALID` | A byte sequence is not decodable in the declared character encoding — names the variable and the value |
| `PULSE_SPSS_EXPORT_UNSUPPORTED` | An SPSS output target was requested — Pulse cannot write `.sav` |

Parse-stage diagnostics (dictionary walk, data pass) carry `record_type`
and `offset` details pinpointing where in the file they were raised, and
the `PULSE_SPSS_ZSAV_*` pair additionally carries `block`;
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
