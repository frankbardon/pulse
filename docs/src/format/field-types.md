# Field Types

**Audience:** anyone designing a cohort schema, decoding a `.pulse`
file by hand, or trying to understand which type to pick for a column.

Pulse supports **17 field types**, each with a fixed type byte, a
fixed (or bit-packed) byte size, and well-defined semantics. The full
list, mirrored from
[CLAUDE.md → All 17 field types](https://github.com/frankbardon/pulse/blob/main/CLAUDE.md#all-17-field-types):

> **LLM agents using MCP:** see the `cohort-schema-design` skill via
> `pulse_skills_get` — it covers nullability, bit-packing trade-offs,
> and "which type to pick" with MCP-side examples.

## The catalog

| Type | Byte value | ByteSize | Notes |
|---|---|---|---|
| `u8`                  | 0  | 1  | Unsigned 8-bit integer |
| `u16`                 | 1  | 2  | Unsigned 16-bit integer |
| `u32`                 | 2  | 4  | Unsigned 32-bit integer |
| `u64`                 | 3  | 8  | Unsigned 64-bit integer |
| `f32`                 | 4  | 4  | 32-bit IEEE 754 float |
| `f64`                 | 5  | 8  | 64-bit IEEE 754 float |
| `nullable_bool`       | 6  | 0  | Bit-packed tri-state (null/true/false) |
| `nullable_u4`         | 7  | 0  | Bit-packed, 4-bit nullable unsigned |
| `nullable_u8`         | 8  | 1  | Nullable 8-bit unsigned |
| `nullable_u16`        | 9  | 2  | Nullable 16-bit unsigned |
| `date`                | 10 | 4  | Date as 32-bit value |
| `packed_bool`         | 11 | 0  | Bit-packed boolean |
| `categorical_u8`      | 12 | 1  | Categorical with up to 256 dictionary entries |
| `categorical_u16`     | 13 | 2  | Categorical with up to 65,536 entries |
| `categorical_u32`     | 14 | 4  | Categorical with up to 4,294,967,295 entries |
| `decimal128`          | 15 | 16 | Fixed-point exact decimal; per-field `(precision, scale)` ≤ (38, 38) |
| `nullable_decimal128` | 16 | 16 | `decimal128` plus an `INT128_MIN` null sentinel |

The Go source-of-truth for this table is
[`encoding/field_type.go`](https://github.com/frankbardon/pulse/blob/main/encoding/field_type.go);
the `FieldType` enum's iota order is the byte-value order above.

## Type families

### Plain integers and floats

`u8`, `u16`, `u32`, `u64`, `f32`, `f64`. Standard little-endian
encoding, full range, no null sentinel. Use these when you know the
column never carries a missing value.

### Nullable integers

`nullable_u8`, `nullable_u16`, `nullable_u4`, `nullable_bool`. Each
reserves one in-band value (or one in-band bit pattern) to mean
"null". For the byte-sized variants the encoding is straightforward;
for the sub-byte variants (`nullable_u4`, `nullable_bool`,
`packed_bool`) Pulse packs multiple fields into shared bytes — see
[Record Layout → Bit-packing](records.md#bit-packing).

`ByteSize()` returns `0` for the bit-packed types because they don't
allocate whole bytes of their own; the schema reader uses `BitPosition`
to locate them within shared bytes.

### Date

`date` is a 32-bit count of days since the Unix epoch. The range is
~5.8 million years on either side of 1970 — effectively unbounded for
real data.

### Categoricals

`categorical_u8`, `categorical_u16`, `categorical_u32`. Each stores
its string-to-ID mapping inline as a dictionary block immediately
after the field's schema entry. Pick the smallest variant that fits
your cardinality (Pulse's import path auto-selects during inference).

Dictionary mechanics are documented in
[Dictionary Blocks](dictionaries.md).

### Decimal128

`decimal128` and `nullable_decimal128` are 16-byte fixed-point decimal
numbers. Each field carries a per-field `(precision, scale)` pair
written into the schema after the description; precision and scale
both top out at 38 (`PULSE_DECIMAL_OVERFLOW`, `PULSE_DECIMAL_PRECISION_LOSS`).

Use these for currency and any other column where IEEE-754 rounding
is not acceptable. See the `financial-cohorts` skill for full
semantics including banker's rounding and divide-by-zero policy.

## Unknown type bytes

The schema reader rejects unknown `FieldType` bytes at parse time
with `ENCODING_INVALID`. This is the same fail-loud strategy as the
header version check: a file written by a future binary that
introduced a new type fails immediately at schema parse, not later
during row decode where the corruption could go unnoticed.

## What you can do with each type

| Concern | Source |
|---|---|
| Which aggregators are meaningful on which types | `skills/aggregation-design.md` (LLM) / [api process](../cli/api-process.md) (CLI) |
| Decimal arithmetic semantics | `skills/financial-cohorts.md` (LLM) |
| Categorical dictionary limits | [Dictionary Blocks](dictionaries.md) |
