---
name: type-datetime
kind: type
description: Second-granularity instant stored as signed epoch seconds (int64, negative = pre-1970), naive UTC.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 8 bytes per record, little-endian. Not bit-packed. Stride contributes 8 bytes to `Schema.RecordByteSize`. On the wire it is a two's-complement **signed int64** holding whole SECONDS since 1970-01-01T00:00:00Z (read a raw `ReadFieldValue` word with `encoding.DateTimeSeconds`). **Not interchangeable with `date`** (4 bytes of epoch DAYS) — swapping the two rescales every value by 86,400.

## Range

The full signed `int64` second range, so pre-1970 instants (negative counts) round-trip losslessly through `encoding.ParseDateTime` / `FormatDateTime`, and order, filter, aggregate and export correctly across the epoch. Canonical text form is `2006-01-02T15:04:05Z` (`encoding.CanonicalDateTimeLayout`); accepted inputs are `encoding.DateTimeFormats`. Ambiguous slash forms (`03/04/2024`) and date-only literals are rejected with `ENCODING_INVALID`.

Honest limits: resolution is **seconds** — a fraction floors toward the past (native Arrow/Parquet timestamps warn `PULSE_IMPORT_TIMESTAMP_TRUNCATED`); keep microseconds in `u64`. Timezone is **naive UTC**: an offset-bearing literal is normalised to the same instant and the offset is discarded (`...T10:11:12+02:00` → `...T08:11:12Z`). Export `--tz Zone` renders the same instant back with a local offset (`...T08:00:00+05:30`). The date-family groupers and filters accept the type and truncate to the UTC calendar day via `encoding.DateTimeToDay`, flooring toward the past (`1969-12-31T23:59:59Z` is day −1).

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. No in-band sentinel — `0` is the epoch instant, not null. `IsNumericForAnalytics()` is `true` (the second count aggregates and regresses directly, null-skipped); `IsNumeric()` is `false`. Index-keyable — key literals resolve through `ParseDateTime`, never `ParseFloat`.

## Dictionary

Absent. Datetimes are never dictionary-encoded.

## See

- Skill: `cohort-schema-design` (Field-type matrix), `grouper-design`.
- Cross-link: `type-date` for day resolution, `type-u64` for sub-second timestamps.
