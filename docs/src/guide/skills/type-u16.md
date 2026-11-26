```yaml
name: type-u16
kind: type
description: 2-byte unsigned integer; fits cardinals through 65,535.
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 2 bytes per record, little-endian. Not bit-packed.

## Range

`0..65,535`. Inference picks `u16` for sampled integers in 256..65,535; a later value past the rung promotes the field (`PULSE_IMPORT_WIDTH_PROMOTED`). Fits medium counts, scores, codes above `u8`'s ceiling.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap (field `i` → byte `i/8`, bit `i%8`). `0` is the integer zero, never null. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators skip nulls.

## Dictionary

Absent. For a string-labelled field with up to 65,536 distinct values use `categorical_u16` (same width, plus an inline dictionary, so `pulse_inspect` and record rows show labels).

## See

- Skill: [`cohort-schema-design`](cohort-schema-design.md) (Field-type matrix, Selection heuristics).
- Cross-link: [`type-categorical-u16`](type-categorical-u16.md).
