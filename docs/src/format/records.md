# Record Layout

**Audience:** anyone hand-decoding row data or implementing a
non-Go reader. The schema block ends; record data starts immediately
after.

Records are **fixed-width**. Every row in a cohort occupies the same
number of bytes, computed from the schema's field types. Variable-width
data (strings) lives in the schema (as categorical dictionaries) or is
not directly supported.

> **LLM agents using MCP:** the record byte layout is an implementation
> detail the MCP surface hides — there is no LLM-facing skill for it.
> The MCP tools operate on the inspect / process / sample
> abstractions.

## Computing record size

Record size is the sum of `FieldType.ByteSize()` over all schema
fields, plus padding bytes that share bits between sub-byte fields.
For non-packed types, `ByteSize()` returns the obvious value
(`u32` = 4, `f64` = 8, `decimal128` = 16); for packed types
(`packed_bool`, `nullable_bool`, `nullable_u4`), `ByteSize()` returns
`0` and the field shares a byte with adjacent packed fields.

The writer (`encoding/record.go`) lays out fields in the order they
appear in the schema; the reader walks the same order with the
per-field `ByteOffset` and `BitPosition` recorded in the schema.

## Encoding per type

From `WriteFieldValue` / `ReadFieldValue` in
[`encoding/record.go`](https://github.com/frankbardon/pulse/blob/main/encoding/record.go):

| Type family | Encoding |
|---|---|
| `u8` / `nullable_u8` / `categorical_u8`     | 1 byte, unsigned |
| `u16` / `nullable_u16` / `categorical_u16`  | 2 bytes, little-endian unsigned |
| `u32` / `date` / `categorical_u32`          | 4 bytes, little-endian unsigned |
| `u64`                                        | 8 bytes, little-endian unsigned |
| `f32`                                        | 4 bytes, little-endian IEEE 754 |
| `f64`                                        | 8 bytes, little-endian IEEE 754 |
| `decimal128` / `nullable_decimal128`         | 16 bytes, little-endian two's-complement integer (scaled by `10^scale`); null sentinel is `INT128_MIN` for the nullable variant |
| `packed_bool` / `nullable_bool` / `nullable_u4` | Bit-packed — see below |

## Bit-packing

Sub-byte types share whole bytes with their packed neighbours. The
schema records both `ByteOffset` (the shared byte's offset) and
`BitPosition` (which bit slot within that byte).

- **`packed_bool`** — 1 bit (true/false).
- **`nullable_bool`** — 2 bits (one null bit, one value bit) for the
  tri-state encoding.
- **`nullable_u4`** — 5 bits (one null bit, four value bits) for the
  nullable 4-bit unsigned encoding.

The writer aligns these into shared bytes from low bit to high bit;
adjacent packed fields stack into the same byte until the byte is
full, after which a new byte begins. `ByteSize() == 0` is the schema
reader's signal that a field type shares bytes — non-zero ByteSize
fields never share.

## Null sentinels

| Type | Null encoding |
|---|---|
| `nullable_u8`         | `0xFF` |
| `nullable_u16`        | `0xFFFF` |
| `nullable_u4`         | Dedicated bit pattern within the packed byte |
| `nullable_bool`       | Dedicated bit within the packed byte |
| `nullable_decimal128` | `INT128_MIN` (`0x8000…0000`) |

`u32`, `u64`, `f32`, `f64`, `date`, `decimal128` (non-nullable), and
all categoricals are **non-nullable** — the import path either coerces
or rejects rows with missing values (`PULSE_IMPORT_ROW_ERROR`). Pick
the `nullable_*` variant when you need to preserve the difference
between "zero" and "missing".

## Reading a record

The Go decoder lives at `encoding.Reader` /
`encoding.ReadRecord(*Schema, []byte)`. A non-Go reader can follow
the same recipe:

1. Compute record size from the schema.
2. Read `record_size` bytes.
3. For each schema field in declaration order:
   - If `ByteSize() > 0`, decode the value at the field's `ByteOffset`.
   - If `ByteSize() == 0`, decode the bit slot at
     `(ByteOffset, BitPosition)` using the type's bit-pattern rules.

## Forward compatibility

Records carry no type tag — they're a packed binary blob whose
interpretation comes entirely from the schema block. That's why the
file's format version (in the [header](header.md)) and unknown
field-type bytes (in the [schema block](schema-block.md)) both fail
loud at parse time: the records themselves cannot self-correct, so
the format gates everything before record data is observed.
