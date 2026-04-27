---
name: cohort-schema-design
description: Choosing types, nullability, bit-packing tradeoffs
type: guide
applies_to: inspect, predict
---

# Cohort Schema Design

## Overview

Every `.pulse` cohort begins with a schema that defines each field's name, type, description, and nullability. Choosing the right field type is critical for storage efficiency, query performance, and semantic correctness.

## The 15 Field Types

Pulse supports exactly 15 field types, organized into four categories: unsigned integers, floating point, nullable/packed, and categorical.

### Unsigned Integer Types

#### u8

An 8-bit unsigned integer (0..255). Use for small counters, age values, ordinal scales, or any non-negative integer fitting in one byte.

- Byte size: 1
- Range: 0 to 255

#### u16

A 16-bit unsigned integer (0..65535). Use for larger counters, year values, or medium-range non-negative integers.

- Byte size: 2
- Range: 0 to 65,535

#### u32

A 32-bit unsigned integer (0..4,294,967,295). Use for record identifiers, large counts, or epoch-day date values.

- Byte size: 4
- Range: 0 to 4,294,967,295

#### u64

A 64-bit unsigned integer. Use for very large identifiers, timestamps as integer microseconds, or hash values.

- Byte size: 8
- Range: 0 to 18,446,744,073,709,551,615

### Floating Point Types

#### f32

A 32-bit IEEE 754 float. Use for measurements where 7 significant digits of precision suffice (e.g., temperature, weight).

- Byte size: 4
- Precision: ~7 significant digits

#### f64

A 64-bit IEEE 754 double. Use for high-precision measurements, financial calculations, or computed scores.

- Byte size: 8
- Precision: ~15 significant digits

### Nullable and Packed Types

These types use bit-packing to share bytes across adjacent fields, reducing per-record storage.

#### nullable_bool

A tri-state boolean: true, false, or null. Uses 2 bits (1 value bit + 1 null bit) packed alongside adjacent fields.

- Byte size: 0 (bit-packed)
- Values: true, false, null

#### nullable_u4

A 4-bit unsigned integer with a null sentinel. Range 0..14 with 15 reserved as null. Ideal for small ordinal scales with missing values (e.g., Likert 1-5).

- Byte size: 0 (bit-packed)
- Range: 0 to 14 (15 = null)

#### nullable_u8

An 8-bit unsigned integer with a separate null bit. Range 0..255 with independent null tracking.

- Byte size: 1
- Range: 0 to 255, plus null

#### nullable_u16

A 16-bit unsigned integer with a separate null bit. Range 0..65535 with independent null tracking.

- Byte size: 2
- Range: 0 to 65,535, plus null

#### date

A date stored as a 32-bit epoch-day offset. Represents calendar dates without time components.

- Byte size: 4
- Range: dates relative to epoch

#### packed_bool

A simple boolean that uses 1 bit, packed alongside adjacent fields. No null support; every record must have a value.

- Byte size: 0 (bit-packed)
- Values: true, false

### Categorical Types

Categorical fields store string values as dictionary-encoded integers. The dictionary is stored in the schema header.

#### categorical_u8

A categorical field with up to 256 distinct values, encoded as a u8 index into the dictionary.

- Byte size: 1
- Max entries: 256

#### categorical_u16

A categorical field with up to 65,536 distinct values, encoded as a u16 index.

- Byte size: 2
- Max entries: 65,536

#### categorical_u32

A categorical field with up to 4,294,967,295 distinct values, encoded as a u32 index.

- Byte size: 4
- Max entries: 4,294,967,295

## Nullability

Nullability in Pulse is type-level, not field-level. A field is nullable if and only if its type supports null:

- **Always nullable**: `nullable_bool`, `nullable_u4`, `nullable_u8`, `nullable_u16`
- **Never nullable**: all other types

When importing data with missing values, choose a nullable type or define a sentinel value convention.

## Bit-Packing

The packed types (`packed_bool`, `nullable_bool`, `nullable_u4`) share bytes with adjacent packed fields. The encoder groups consecutive packed fields and allocates the minimum number of bytes to hold all their bits.

This means reordering fields in a schema can change the byte layout. Place packed fields adjacent to each other for optimal packing.

## Description Quality

Every field should have a description that explains what the field represents, its units, and any domain-specific semantics. Descriptions are limited to 1000 bytes and are checked for quality during import.

Good descriptions help downstream consumers (both human and LLM) understand the data without external documentation.

## Schema Template Workflow

Use `pulse import schema-template` to generate a starting schema from a CSV/TSV file. The template infers types from sample rows and provides a JSON schema you can edit before importing.
