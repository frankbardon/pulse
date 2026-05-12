---
name: cohort-schema-design
description: Choosing types, nullability, bit-packing tradeoffs
type: guide
applies_to: inspect, predict
---

# Cohort Schema Design

<skill_overview>
Schema design determines storage layout, encoding width, and downstream aggregation behavior for a `.pulse` cohort. Invoke this skill when authoring or reviewing a schema template, picking field types, or planning bit-packed runs.
</skill_overview>

<reference>
## Field types (all 19)

| Type | Byte | Notes |
|---|---|---|
| `u8` | 1 | Unsigned 8-bit (0..255) |
| `u16` | 2 | Unsigned 16-bit (0..65,535) |
| `u32` | 4 | Unsigned 32-bit (0..~4.29B) |
| `u64` | 8 | Unsigned 64-bit |
| `f32` | 4 | IEEE 754, ~7 significant digits |
| `f64` | 8 | IEEE 754, ~15 significant digits |
| `nullable_bool` | 0 | Bit-packed tri-state (null/true/false) |
| `nullable_u4` | 0 | Bit-packed nibble; range 0..14, 15 = null |
| `nullable_u8` | 1 | 8-bit unsigned with separate null bit |
| `nullable_u16` | 2 | 16-bit unsigned with separate null bit |
| `date` | 4 | Epoch days since Unix epoch (1970-01-01), no time component |
| `packed_bool` | 0 | Bit-packed boolean, no null support |
| `categorical_u8` | 1 | Dictionary-encoded, ≤256 entries |
| `categorical_u16` | 2 | Dictionary-encoded, ≤65,536 entries |
| `categorical_u32` | 4 | Dictionary-encoded, ≤~4.29B entries |
| `decimal128` | 16 | Fixed-point exact decimal, per-field (precision, scale) |
| `nullable_decimal128` | 16 | `decimal128` plus an INT128_MIN null sentinel |
| `point_f64` | 16 | Packed (lat, lon) f64 pair for geo points |
| `h3_cell` | 8 | Uber H3 cell index as `uint64` |
</reference>

<reference>
## Backwards compatibility

Files containing `decimal128`, `nullable_decimal128`, `point_f64`, or `h3_cell` fields are unreadable by pre-imp19 binaries by design. The reader rejects unknown FieldType bytes at schema parse time with `ENCODING_INVALID` — failure is loud and immediate, not silent at row decode. The format version byte stays at `0x01`; there is no flag-day version bump.
</reference>

<reference>
## Type selection heuristics

- Counts and IDs: pick the smallest unsigned width that fits the maximum value (`u8` < `u16` < `u32` < `u64`).
- Floats: prefer `f32` for measurements where ~7 significant digits suffice; use `f64` for financial math, computed scores, or wide dynamic range.
- Booleans without nulls: `packed_bool`. With nulls: `nullable_bool`.
- Small ordinals with missing values (Likert 1-5, grades): `nullable_u4`.
- Calendar dates: `date`. For sub-day timestamps, store as `u64` microseconds.
- Strings: always categorical. Pick the width by expected distinct cardinality.
</reference>

<reference>
## Categorical width selection

| Distinct values | Width |
|---|---|
| ≤ 256 | `categorical_u8` |
| ≤ 65,536 | `categorical_u16` |
| up to ~4.29B | `categorical_u32` |

Exceeding the chosen width raises `PULSE_IMPORT_CATEGORICAL_OVERFLOW`; an unbounded inferred dictionary raises `PULSE_IMPORT_CATEGORICAL_UNBOUNDED`.
</reference>

<rule severity="should" topic="bit-packing">
## Bit-packing rules

- `packed_bool`, `nullable_bool`, and `nullable_u4` return `ByteSize() == 0` and share bytes with adjacent packed fields.
- The encoder coalesces a run of consecutive packed fields into the minimum number of bytes; place packed fields next to each other for optimal layout.
- Reordering schema fields can change byte offsets even when types are unchanged.
</rule>

<rule severity="must" topic="descriptions">
## Descriptions

- Capped at 1000 bytes per field; longer values raise `PULSE_IMPORT_DESCRIPTION_TOO_LONG`.
- Empty, sub-10-character, or generic descriptions ("n/a", "tbd", "unknown", "field", "data", "value", "column") trigger `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (warning by default, error under `--strict`).
- Style: concise, third-person, present-tense; state what the field represents, its units, and any domain semantics.
</rule>

<reference>
## Schema-template workflow

Import is a CLI / library operation; there is no `pulse_import` MCP tool today. Point a human at https://frankbardon.github.io/pulse/cli/api-inspect.html plus the import chapters in mdBook for the `schema-template` -> edit -> import flow.
</reference>

<reference>
## Inspect post-import

Call `pulse_inspect` with `{"path": "FILE.pulse"}` to verify field types, byte offsets, descriptions, and (truncated) categorical dictionaries. The MCP handler reads only the header — it is cheap regardless of cohort size.
</reference>
