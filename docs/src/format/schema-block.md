# Schema Block

**Audience:** anyone decoding a `.pulse` file by hand or writing a
non-Go reader. The schema block follows the 9-byte
[header](header.md) and carries one descriptor per column.

> **From CLAUDE.md, byte-layout invariants for `.pulse` files,** plus
> the on-disk format documented in
> [`encoding/schema.go`](https://github.com/frankbardon/pulse/blob/main/encoding/schema.go).

## Top-level shape

```
u16 field_count
field_record × field_count
```

Each `field_record` is variable-width (it includes UTF-8 name and
description strings, and may include a categorical dictionary or
decimal/H3 metadata). The reader walks them sequentially.

## Per-field record

In write order — see `WriteSchema` /  `ReadSchema` in
`encoding/schema.go`:

| # | Field | Size | Encoding |
|---|---|---|---|
| 1 | type            | 1 byte  | `FieldType` byte (see [Field Types](field-types.md)) |
| 2 | name_length     | 2 bytes | u16 little-endian |
| 3 | name            | name_length bytes | UTF-8 |
| 4 | byte_offset     | 4 bytes | u32 LE — offset within a record |
| 5 | bit_position    | 1 byte  | u8 — bit position within `byte_offset` (bit-packed types only) |
| 6 | csv_column_idx  | 2 bytes | u16 LE — source column index at import time |
| 7 | description     | 2 bytes length + UTF-8 | Capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`) |
| 8 | (decimal only) precision | 1 byte | `decimal128` and `nullable_decimal128` only |
| 9 | (decimal only) scale | 1 byte | same |
| 10 | (h3 only) resolution | 1 byte | `h3_cell` only; sentinel `0xFF` = unspecified |
| 11 | (categorical only) dictionary | variable | See [Dictionary Blocks](dictionaries.md) |

Order matters: every reader walks these in the listed order, so a
malformed record stops the parse with `ENCODING_INVALID`.

## Byte offsets and bit positions

`byte_offset` is the offset of this field's first byte within a
record. For bit-packed types (`packed_bool`, `nullable_bool`,
`nullable_u4`), `byte_offset` plus `bit_position` together locate the
field's bits within a byte that may be shared with adjacent fields.

For non-packed types, `bit_position` is always `0`.

Record layout mechanics — including the bit-packing rule, record-size
computation, and how the encoder packs adjacent sub-byte fields — are
in [Record Layout](records.md).

## Conditional trailers

Three trailers attach only to specific field types:

- **`decimal128` / `nullable_decimal128`** get a `(precision, scale)`
  pair (`u8`, `u8`). Both ≤ 38.
- **`h3_cell`** gets a single `u8` resolution byte. `0xFF` means
  "unspecified" (recorded at import time when the source didn't carry
  resolution metadata).
- **Categorical types** (`categorical_u8`, `categorical_u16`,
  `categorical_u32`) get a full dictionary block in line — see
  [Dictionary Blocks](dictionaries.md).

A field with none of the above writes nothing after the description.

## Field descriptions

The description string is UTF-8 with a 2-byte length prefix. The
import path rejects descriptions longer than 1000 bytes
(`PULSE_IMPORT_DESCRIPTION_TOO_LONG`) and warns on low-quality
descriptions (empty, under 10 characters, or generic words like
`"n/a"`, `"tbd"`, `"unknown"`, `"field"`, `"data"`, `"value"`,
`"column"`) — that warning is `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`,
upgraded to an error under `--strict`.

When the description is empty, `pulse cohort inspect` synthesises a
fallback string ("Categorical field: <name>" or "Numeric field:
<name>") with `description_source = "synthesized"`. The original
bytes on disk remain empty.

## Reader behaviour

`encoding.ReadSchema` is intentionally strict:

- Field count limit comes from the u16 prefix (max 65,535 fields).
- Unknown type bytes fail loud (`ENCODING_INVALID`).
- Truncated records fail loud at the first short read.
- The reader produces a `*encoding.Schema` with one
  `encoding.Field` per record; `Schema.Field(name)` looks fields up by
  name.

After the schema block, record data starts at the file's first byte
past the schema. The record layout is documented in
[Record Layout](records.md).
