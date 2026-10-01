---
name: type-u16
kind: type
description: 2-byte unsigned integer; fits cardinals through 65,535.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 2 bytes per record, little-endian on the wire. Not bit-packed. Stride contributes 2 bytes to `Schema.RecordByteSize`; reader uses `binary.LittleEndian.Uint16`.

## Range

`0..65,535` (16-bit unsigned). Right-sized for medium cardinals: ZIP prefixes, score columns capped at 100, modest counts, ordinals beyond `u8`'s ceiling but well below 2^32.

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap (one bit at `i/8`, `i%8`). `0` is the integer zero — never a sentinel. Counts as `IsNumeric()` and `IsNumericForAnalytics()`; null-skip semantics apply uniformly.

## Dictionary

Absent. If the underlying column is string-labeled and the distinct count is `≤65,536`, prefer `categorical_u16` — same on-wire width, plus an inline dictionary that lets `pulse_inspect` round-trip labels.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Selection heuristics).
- Cross-link: `type-categorical-u16`.
