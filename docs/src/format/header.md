# Header Layout

**Audience:** anyone reading or writing `.pulse` files by hand (forensics,
custom readers, debugging a truncated file). The Go library handles all of
this for you; this page documents the wire format.

The header is fixed-size: **9 bytes**, consisting of an 8-byte magic
identifier and a 1-byte format version.

> **LLM agents using MCP:** see the `cohort-schema-design` skill via
> `pulse_skills_get`. It speaks in field-type semantics rather than byte
> layout; this page covers the bytes.

## Constants

These live in [`encoding/header.go`](https://github.com/frankbardon/pulse/blob/main/encoding/header.go) (`MaxFormatVersion` in `internal/encoding/preamble.go`):

| Name | Value | Purpose |
|---|---|---|
| `MagicBytes`     | `[8]byte{'P','U','L','S','E', 0x00, 0x00, 0x00}` | 8-byte identifier; rejects non-Pulse files |
| `FormatVersionV1` | `0x01` | Field descriptors (with inline dictionaries), then records |
| `FormatVersionV2` | `0x02` | Adds a length-prefixed schema extension block carrying [parent groups](parent-groups.md), [vector dims](field-types.md#vector-fields) and the [cohort default zone](schema-block.md#cohort-default-zone-zone-tag-3) (see [Schema Block](schema-block.md)) |
| `FormatVersion`  | `0x01` | Baseline: what writers emit for a schema that uses no `0x02` feature |
| `MaxFormatVersion` | `0x02` | Newest version this binary reads and writes |
| `HeaderSize`     | `9` | Total header byte count |

## Byte layout

```
Offset  Length  Field
------  ------  -----
0       8       Magic: "PULSE\0\0\0"
8       1       Format version: 0x01 or 0x02
9       —       Schema block begins here
```

That's the entire fixed header. The schema block immediately follows;
see [Schema Block](schema-block.md).

## Version semantics

The format version is **single-byte**. The accepted set is
`{0x01, 0x02}`. `encoding.ReadHeader` returns the version it read, and
the caller must hand it to `encoding.ReadSchema` — the schema block's
layout depends on it, and records begin immediately after the schema
block with no terminator (`internal/encoding.ReadPreamble` does both in one call).
Any other version byte is rejected with `ENCODING_INVALID`:

```
ENCODING_INVALID: unsupported pulse format version: ...
{"version": <byte>, "supported_versions": [1, 2]}
```

This is the fail-loud guard against silently mis-decoding a file written
by a newer binary. A forward-incompatible change bumps the version; the
older reader stops at header parse instead of producing wrong rows. A
binary that predates `0x02` refuses every `0x02` file this way.

**Every supported version stays readable.** A checked-in `0x01` golden
(`encoding/testdata/format_v1.pulse`) and a `0x02` one
(`format_v2_grouped.pulse`) are read by every build in CI.

**The version written is a function of schema content, never a global
flag** (`Schema.RequiredFormatVersion`, `internal/encoding.WritePreamble`). A schema
that uses no `0x02` feature is written at `0x01`. Only a schema that declares a
[parent group](parent-groups.md), contains a [vector field](field-types.md#vector-fields)
or declares a [default zone field](schema-block.md#cohort-default-zone-zone-tag-3)
is written at `0x02`.

The envelope `format_version` (`"1.1"`) that all CLI `--json` output
carries is unrelated — it tracks the JSON output schema, not the binary
file format.

## Hexdump sanity check

A freshly-written `.pulse` file starts with:

```
00000000  50 55 4c 53 45 00 00 00  01  ..  ..  ..  ..  ..
          |P  U  L  S  E  \0 \0 \0|ver| schema starts here (ver 01 or 02)
```

Two other openings are not this header. `PK\x03\x04` is a
[shard archive](../internals/managing-shard-archives.md) (a Zip64 whose
`_schema.pulse` member carries the header). `28 b5 2f fd` is a zstd
[transfer artifact](transfer.md), refused with `PULSE_COHORT_COMPRESSED`
until `pulse import transfer` decompresses it. Anything else is not a
cohort, or is truncated or corrupted; see
[Troubleshooting](../ops/troubleshooting.md).

## What comes next

The schema block follows the header. Read it as documented in
[Schema Block](schema-block.md); it carries per-field descriptors,
inline categorical and set dictionaries, and decimal precision and scale. After the
schema, fixed-width records start — see [Record Layout](records.md).
