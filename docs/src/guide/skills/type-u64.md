```yaml
name: type-u64
kind: type
description: 8-byte unsigned integer; for very wide keys and sub-second timestamps.
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 8 bytes per record, little-endian. Not bit-packed.

## Range

`0..2^64 − 1`. Fits very wide surrogate keys, hash-derived identifiers, and sub-second timestamps as epoch microseconds (`datetime` stores whole seconds). Inference picks it for sampled integers past 2^32−1; the integer ladder ends here, so a later negative or fractional value is a row error.

Exact where identity matters: index keys, join `on` keys, rendered rows (point-lookup, sample and `process` record rows, including a lag / lead window copy: Go `uint64`, JSON the exact integer) and expression comparisons (`id == 9007199254740993`, `id < params.k`, `id in [...]`, against an integer, a float or another `u64`; also via `$env.id`, a `let`, or an `ATTR_FORMULA` derived field whose expression is just `id`) use the stored word. Arithmetic (expressions included), windows and `sort` read a `float64` echo, exact only below 2^53 (ids above it can tie).

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. `0` is the integer zero, never null. `IsNumeric()` and `IsNumericForAnalytics()` are both `true`; aggregators skip nulls.

## Dictionary

Absent. There is no `categorical_u64`; labelled enums top out at `categorical_u32`.

## See

- Skill: [`cohort-schema-design`](cohort-schema-design.md) (Field-type matrix, Selection heuristics).
- Cross-link: [`type-datetime`](type-datetime.md) (whole seconds), [`type-date`](type-date.md) (days).
