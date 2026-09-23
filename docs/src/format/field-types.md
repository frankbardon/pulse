# Field Types

**Audience:** anyone designing a cohort schema, decoding a `.pulse`
file by hand, or trying to understand which type to pick for a column.

Every field type has a fixed type byte, a fixed (or bit-packed) byte
size, and well-defined semantics. The registry is
[`encoding/field_type.go`](https://github.com/frankbardon/pulse/blob/main/encoding/field_type.go)
— the `FieldType` const block gives the byte values (iota order),
`ByteSize()` the widths and `String()` the names below. For the live
list at runtime, read `cohort_types` out of `pulse manifest --json`
rather than counting rows in this page.

> **LLM agents using MCP:** see the `cohort-schema-design` skill via
> `pulse_skills_get` for the same matrix plus "which type to pick",
> and `type-<name>` (e.g. `type-set-u256`) for one type in depth.

## Nullability is orthogonal to type

**There are no `nullable_*` types.** Any field of any type may be
marked nullable (`encoding.Field.Nullable`), which sets the schema's
per-field nullable flag byte and enrols the field in the **per-record
null bitmap** described in [Record Layout](records.md#the-null-bitmap).
Nullability never changes a field's type byte, its width, or the
operator the engine infers for it.

## The catalog

Type bytes are the `FieldType` iota order. Note that it is **not**
grouped by family — `datetime` (17) and the two wide set rungs (18, 19)
were appended after the original block, and appending is the only way
to add a type without invalidating every existing file.

| Type | Byte | ByteSize | Notes |
|---|---|---|---|
| `u8`              | 0  | 1  | Unsigned 8-bit integer |
| `u16`             | 1  | 2  | Unsigned 16-bit integer |
| `u32`             | 2  | 4  | Unsigned 32-bit integer |
| `u64`             | 3  | 8  | Unsigned 64-bit integer |
| `f32`             | 4  | 4  | 32-bit IEEE 754 float |
| `f64`             | 5  | 8  | 64-bit IEEE 754 float |
| `u4`              | 6  | 0  | **Bit-packed** 4-bit unsigned (0–15); shares a byte with a neighbour |
| `date`            | 7  | 4  | Epoch **DAYS** as u32 |
| `packed_bool`     | 8  | 0  | **Bit-packed** boolean; up to 8 share one byte |
| `categorical_u8`  | 9  | 1  | Dictionary-backed single value; ≤ 256 entries |
| `categorical_u16` | 10 | 2  | ≤ 65,536 entries |
| `categorical_u32` | 11 | 4  | ≤ 4,294,967,295 entries |
| `decimal128`      | 12 | 16 | Fixed-point exact decimal; per-field `(precision, scale)`, both ≤ 38 |
| `set_u8`          | 13 | 1  | Multi-select bitmask over an inline dictionary of ≤ 8 labels |
| `set_u16`         | 14 | 2  | ≤ 16 labels |
| `set_u32`         | 15 | 4  | ≤ 32 labels |
| `set_u64`         | 16 | 8  | ≤ 64 labels |
| `datetime`        | 17 | 8  | Epoch **SECONDS** as u64 |
| `set_u128`        | 18 | 16 | ≤ 128 labels; two little-endian 64-bit words, `words[0]` = bits 0–63 |
| `set_u256`        | 19 | 32 | ≤ 256 labels; four little-endian words. Widest set rung — a hard ceiling |

## Type families

### Plain integers and floats

`u8`, `u16`, `u32`, `u64`, `f32`, `f64`. Standard little-endian
encoding, full range, no in-band null sentinel. Mark the field nullable
if the column carries missing values.

### Bit-packed types

`u4` (a 4-bit unsigned) and `packed_bool` (one bit) both report
`ByteSize() == 0` and share bytes with adjacent packed fields; the
schema's `BitPosition` locates them. `ByteSize() == 0` is the reader's
signal that a field shares bytes — a non-zero `ByteSize` field never
does. See [Record Layout → Bit-packing](records.md#bit-packing).

### Temporal types

`date` is a u32 count of **days** since the Unix epoch. `datetime` is a
u64 count of **seconds** since the same epoch.

**They are never interchangeable: swapping them rescales every value by
86,400.** Everything downstream of the operator boundary speaks epoch
days only, so a `datetime` column is day-truncated exactly once there
(`encoding.DateTimeToDay`, toward the past, naive UTC).

### Categoricals

`categorical_u8`, `categorical_u16`, `categorical_u32` store a single
dictionary ID per row and carry the string-to-ID mapping inline as a
dictionary block immediately after the field's schema entry. Pick the
smallest variant that fits your cardinality (import inference does this
for you). See [Dictionary Blocks](dictionaries.md).

### Sets (multi-select bitmasks)

`set_u8`, `set_u16`, `set_u32`, `set_u64`, `set_u128`, `set_u256` carry
the **same inline dictionary block** as a categorical, but the on-wire
payload is a fixed-width bitmask: bit `i` set means dictionary entry
`i` is selected. One row can select any number of labels, which is what
makes these the landing type for an SPSS multiple-dichotomy battery.

- Inference picks the **smallest rung that fits** the option count; a
  column with more than 256 options has no set type at all and stays as
  its constituent columns.
- **An empty mask is a valid value** meaning "answered, selected
  nothing" — it is NOT null, and the two must stay distinct because
  "ticked none of these" and "skipped the question" give different
  denominators.
- The wide rungs (`set_u128`, `set_u256`) exceed the `uint64` value
  API: `ReadFieldValue` / `WriteFieldValue` refuse them with
  `ENCODING_TYPE_MISMATCH` rather than truncating to the low 64 bits
  (`FieldType.IsWideSet()`). Wide masks travel as `encoding.SetMask`.

### Decimal128

`decimal128` is a 16-byte fixed-point decimal. Each field carries a
per-field `(precision, scale)` pair written into the schema after the
description; both top out at 38 (`PULSE_DECIMAL_OVERFLOW`,
`PULSE_DECIMAL_PRECISION_LOSS`).

Use it for currency and any other column where IEEE-754 rounding is
not acceptable. See the `financial-cohorts` skill for full semantics
including banker's rounding and divide-by-zero policy. `decimal128`
nulls ride the bitmap only — there is no in-band sentinel.

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
| Per-type depth, one file per type | `skills/type-<name>.md` (LLM) |
| Decimal arithmetic semantics | `skills/financial-cohorts.md` (LLM) |
| Categorical and set dictionary limits | [Dictionary Blocks](dictionaries.md) |
| Multiple-response set import | [import spss](../cli/import-spss.md) |
