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

These live in [`encoding/header.go`](https://github.com/frankbardon/pulse/blob/main/encoding/header.go):

| Name | Value | Purpose |
|---|---|---|
| `MagicBytes`     | `[]byte{'P','U','L','S','E', 0x00, 0x00, 0x00}` | 8-byte identifier; rejects non-Pulse files |
| `FormatVersion`  | `0x01` (today) | Current `.pulse` wire format |
| `HeaderSize`     | `9` | Total header byte count |

## Byte layout

```
Offset  Length  Field
------  ------  -----
0       8       Magic: "PULSE\0\0\0"
8       1       Format version (currently 0x01)
9       —       Schema block begins here
```

That's the entire fixed header. The schema block immediately follows;
see [Schema Block](schema-block.md).

## Version semantics

The format version is **single-byte**. The reader at
`encoding.ReadHeader` rejects unknown versions with the
`ENCODING_INVALID` error code:

```
ENCODING_INVALID: unsupported pulse format version
{"version": <byte>}
```

This is the fail-loud guard against silently mis-decoding a file written
by a future binary that introduced a new field type or layout change. A
forward-incompatible change bumps the version; the older reader stops at
header parse instead of producing wrong rows.

The current value is `0x01`. The envelope `format_version` (`"1.0"`)
that all CLI `--json` output carries is unrelated — it tracks the
JSON output schema, not the binary file format.

## Hexdump sanity check

A freshly-written `.pulse` file starts with:

```
00000000  50 55 4c 53 45 00 00 00  01  ..  ..  ..  ..  ..
          |P  U  L  S  E  \0 \0 \0|ver| schema starts here
```

If `file path/to/data.pulse` reports "data" (rather than something
plausible) and the first nine bytes don't match the above, the file is
either truncated or corrupted — see
[Troubleshooting](../ops/troubleshooting.md).

## What comes next

The schema block follows the header. Read it as documented in
[Schema Block](schema-block.md); it carries per-field descriptors,
inline categorical dictionaries, and decimal/H3 metadata. After the
schema, fixed-width records start — see [Record Layout](records.md).
