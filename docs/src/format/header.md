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
| `FormatVersionV1` | `0x01` | Original layout — every file written before `0x02` existed |
| `FormatVersionV2` | `0x02` | Adds a length-prefixed schema extension block carrying [parent groups](parent-groups.md) (see [Schema Block](schema-block.md)) |
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

**Old cohorts stay readable forever.** A `0x01` file parses exactly as it
always has, and a checked-in `0x01` golden (`encoding/testdata/format_v1.pulse`)
is read by every build in CI.

**The version written is a function of schema content, never a global
flag** (`Schema.RequiredFormatVersion`, `internal/encoding.WritePreamble`). A schema
that uses no `0x02` feature is written at `0x01`, byte-identical to
every file written before `0x02` existed. Only a schema that declares a
[parent group](parent-groups.md) is written at `0x02`.

The envelope `format_version` (`"1.1"`) that all CLI `--json` output
carries is unrelated — it tracks the JSON output schema, not the binary
file format.

## Hexdump sanity check

A freshly-written `.pulse` file starts with:

```
00000000  50 55 4c 53 45 00 00 00  01  ..  ..  ..  ..  ..
          |P  U  L  S  E  \0 \0 \0|ver| schema starts here (ver 01 or 02)
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
