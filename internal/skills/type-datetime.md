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

The full signed `int64` second range: pre-1970 instants (negative counts) round-trip losslessly through `encoding.ParseDateTime` / `FormatDateTime` and order, filter, aggregate and export correctly across the epoch. Canonical text `2006-01-02T15:04:05Z` (`encoding.CanonicalDateTimeLayout`); accepted inputs `encoding.DateTimeFormats`; ambiguous slash forms (`03/04/2024`) and date-only literals are `ENCODING_INVALID`. Record-row cells use the text form; aggregates stay numbers.

Resolution is **seconds** and the stored instant is **naive UTC** (an offset literal keeps its instant, not its offset). Sub-second flooring, offsets, export zones and day truncation: `time-zones` (Stored instants).

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. No in-band sentinel — `0` is the epoch instant, not null. `IsNumericForAnalytics()` is `true` (the second count aggregates and regresses directly, null-skipped); `IsNumeric()` is `false`. Index-keyable — key literals resolve through `ParseDateTime`, never `ParseFloat`.

## Dictionary

Absent. Datetimes are never dictionary-encoded.

## See

- Skill: `cohort-schema-design` (Field-type matrix), `grouper-design`.
- Cross-link: `time-zones` (local days, import and export zones), `type-date` for day resolution, `type-u64` for sub-second timestamps.
