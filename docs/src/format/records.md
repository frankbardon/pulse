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

Record size (`Schema.RecordByteSize`) is the sum of
`FieldType.ByteSize()` over all schema fields, **plus one byte for
each bit-packed field**, **plus the null bitmap** when the schema
declares any nullable field. For non-packed types `ByteSize()` returns
the obvious value (`u32` = 4, `f64` = 8, `decimal128` = 16,
`set_u256` = 32); the bit-packed types (`u4`, `packed_bool`) return
`0` and are counted as one byte each.

**Stride is a pure function of the type bytes.** The per-field
`ByteOffset` stored in the schema is a convenience for readers, not
the source of truth — a type byte with the wrong width silently
corrupts every downstream offset.

The writer (`encoding/record.go`) lays out fields in the order they
appear in the schema; the reader walks the same order.

## Encoding per type

From `WriteFieldValue` / `ReadFieldValue` in
[`encoding/record.go`](https://github.com/frankbardon/pulse/blob/main/encoding/record.go):

| Type family | Encoding |
|---|---|
| `u8` / `categorical_u8` / `set_u8`    | 1 byte, unsigned |
| `u16` / `categorical_u16` / `set_u16` | 2 bytes, little-endian unsigned |
| `u32` / `date` / `categorical_u32` / `set_u32` | 4 bytes, little-endian unsigned |
| `u64` / `datetime` / `set_u64`        | 8 bytes, little-endian unsigned |
| `f32`                                 | 4 bytes, little-endian IEEE 754 |
| `f64`                                 | 8 bytes, little-endian IEEE 754 |
| `decimal128`                          | 16 bytes, little-endian two's-complement integer (scaled by `10^scale`). Exceeds the `uint64` API — use the 16-byte accessors |
| `set_u128` / `set_u256`               | 16 / 32 bytes, little-endian **64-bit words**, `words[0]` = bits 0–63. Exceeds the `uint64` API — `Read/WriteFieldValue` return `ENCODING_TYPE_MISMATCH` rather than truncating; use `encoding.SetMaskFromBytes` / `PutSetMask` |
| `u4` / `packed_bool`                  | Bit-packed — see below |

A `set_*` payload is a membership bitmask, not a number: bit `i` set
means dictionary entry `i` is selected, and an all-zero mask is a valid
"selected nothing" value distinct from null.

## Bit-packing

The two bit-packed types carry fewer than 8 bits of payload. The
schema records both `ByteOffset` and `BitPosition` (which slot within
that byte the value occupies).

- **`packed_bool`** — 1 bit. Value is `(b >> BitPosition) & 1`.
- **`u4`** — 4 bits. `BitPosition > 0` selects the HIGH nibble
  (`b >> 4`); `BitPosition == 0` selects the low nibble (`b & 0x0F`).

**Each bit-packed field still occupies one whole byte of the record
stride.** `RecordByteSize` adds one byte per bit-packed field and the
decoder advances its cursor by one byte for each — the bits are packed
*within* a field's own byte, not *across* fields. `ByteSize() == 0` is
the reader's signal to use the bit-level API (`ReadBit` / `ReadNibble`)
rather than `ReadFieldValue`, not a signal that the field is free.

## The null bitmap

**There are no `nullable_*` types and no in-band null sentinels.**
Nullability is orthogonal to type: any field may set its schema
nullable flag byte, and nulls then ride a per-record bitmap.

The bitmap is present **iff the schema has at least one nullable
field** (`Schema.HasBitmap()`). When present, every record carries a
trailing `ceil(field_count / 8)` bytes (`Schema.BitmapByteSize()`),
and those bytes are part of the stride.

- Field index `i` → byte `i/8`, bit `i%8`, **LSB-first**.
- Bit set (`1`) means **null**.
- The bitmap covers every field position, not only the nullable ones;
  bits for non-nullable fields are simply never set.
- Helpers: `encoding.ReadBitmap` / `WriteBitmap` / `BitmapIsNull` /
  `BitmapSetNull`.

`decimal128` and `set_*` nulls ride the bitmap only. For a `set_*`
field in particular the bitmap is the ONLY null signal — an all-zero
mask means "selected nothing", which is data, not absence.

## Reading a record

The Go decoder lives at `encoding.Reader` /
`encoding.ReadRecord(*Schema, []byte)`. A non-Go reader can follow
the same recipe:

1. Compute record size from the schema (field widths + one byte per
   bit-packed field + the bitmap, if any).
2. Read `record_size` bytes.
3. Walk the schema fields in declaration order, advancing a cursor:
   - If `ByteSize() > 0`, decode `ByteSize()` bytes at the cursor.
   - If `ByteSize() == 0` (`u4`, `packed_bool`), consume ONE byte and
     decode the bit slot at `BitPosition` within it.
4. If the schema has a bitmap, the last `BitmapByteSize()` bytes of the
   record are it; mark field `i` null when bit `i%8` of byte `i/8` is
   set.

## Forward compatibility

Records carry no type tag — they're a packed binary blob whose
interpretation comes entirely from the schema block. That's why the
file's format version (in the [header](header.md)) and unknown
field-type bytes (in the [schema block](schema-block.md)) both fail
loud at parse time: the records themselves cannot self-correct, so
the format gates everything before record data is observed.
