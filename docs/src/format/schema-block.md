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
description strings, and may include an inline dictionary or decimal
`(precision, scale)` metadata). The reader walks them sequentially.

## Per-field record

In write order — see `WriteSchema` /  `ReadSchema` in
`encoding/schema.go`:

| # | Field | Size | Encoding |
|---|---|---|---|
| 1 | type            | 1 byte  | `FieldType` byte (see [Field Types](field-types.md)) |
| 2 | nullable        | 1 byte  | `1` = the field participates in the per-record null bitmap, `0` = it does not. **Immediately after the type byte** |
| 3 | name_length     | 2 bytes | u16 little-endian |
| 4 | name            | name_length bytes | UTF-8 |
| 5 | byte_offset     | 4 bytes | u32 LE — offset within a record |
| 6 | bit_position    | 1 byte  | u8 — slot within the field's byte (bit-packed types only; `0` otherwise) |
| 7 | csv_column_idx  | 2 bytes | u16 LE — source column index at import time |
| 8 | description     | 2 bytes length + UTF-8 | Capped at 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG`) |
| 9 | (decimal only) precision | 1 byte | `decimal128` only |
| 10 | (decimal only) scale | 1 byte | same |
| 11 | (dictionary types only) dictionary | variable | `categorical_*` and `set_*`. See [Dictionary Blocks](dictionaries.md) |

Order matters: every reader walks these in the listed order, so a
malformed record stops the parse with `ENCODING_INVALID`.

The nullable flag is **not** a type variant. There are no `nullable_*`
field types — any type may carry the flag, and the flag alone decides
whether the schema has a null bitmap at all
([Record Layout → The null bitmap](records.md#the-null-bitmap)).

## Byte offsets and bit positions

`byte_offset` is the offset of this field's first byte within a
record. For the two bit-packed types (`u4`, `packed_bool`),
`byte_offset` plus `bit_position` together locate the field's bits
within its own byte — a bit-packed field still consumes one whole byte
of the record stride.

For every other type, `bit_position` is always `0`.

`byte_offset` is a reader convenience, not the authority: record stride
is derived from the type bytes alone (`Schema.RecordByteSize`).

Record layout mechanics — the bit-packing rule, record-size
computation and the trailing null bitmap — are in
[Record Layout](records.md).

## Conditional trailers

Two trailers attach only to specific field types:

- **`decimal128`** gets a `(precision, scale)` pair (`u8`, `u8`). Both
  ≤ 38.
- **Dictionary-bearing types** (`FieldType.HasDictionary()` — the three
  `categorical_*` and all six `set_*` rungs) get a full dictionary
  block inline; see [Dictionary Blocks](dictionaries.md). A
  dictionary-bearing field with no dictionary still writes an empty
  one (a `u32` zero count), so the trailer's presence is decided by the
  TYPE, never by whether values were seen.

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
fallback string (`Categorical field: <name>` or `Numeric field:
<name>`) with `description_source = "synthesized"`. The original
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
