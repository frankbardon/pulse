---
name: type-datetime
kind: type
description: Second-granularity instant stored as epoch seconds in an 8-byte unsigned slot, naive UTC.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 8 bytes per record, little-endian. Not bit-packed. Stride contributes 8 bytes to `Schema.RecordByteSize`. On the wire it is a two's-complement **signed int64** holding whole SECONDS since 1970-01-01T00:00:00Z (read a raw `ReadFieldValue` word with `encoding.DateTimeSeconds`). **Not interchangeable with `date`** (4 bytes of epoch DAYS) — swapping the two rescales every value by 86,400.

## Range

The full signed `int64` second range, so pre-1970 instants (negative counts) round-trip losslessly through `encoding.ParseDateTime` / `FormatDateTime`, and order, filter, aggregate and export correctly across the epoch. Canonical text form is `2006-01-02T15:04:05Z` (`encoding.CanonicalDateTimeLayout`); accepted inputs are `encoding.DateTimeFormats`. Ambiguous slash forms (`03/04/2024`) and date-only literals are rejected with `ENCODING_INVALID`.

Honest limits: resolution is **seconds** — any fractional part is truncated toward the epoch, so microsecond data still belongs in `u64`. Timezone is **naive UTC**: an offset-bearing literal is normalised to the same instant and the offset is discarded (`...T10:11:12+02:00` → `...T08:11:12Z`). Date operators (`GROUP_DATE`, `GROUP_DATE_RANGES`, `FILTER_DATE_RANGES`) accept the type and truncate to the UTC calendar day via `encoding.DateTimeToDay`, flooring toward the past (`1969-12-31T23:59:59Z` is day −1).

## Null

Orthogonal. `Nullable: true` participates in the per-record null bitmap. No in-band sentinel — `0` is the epoch instant, not null. `IsNumericForAnalytics()` is `true` (the second count aggregates and regresses directly, null-skipped); `IsNumeric()` is `false`. Index-keyable — key literals resolve through `ParseDateTime`, never `ParseFloat`.

## Dictionary

Absent. Datetimes are never dictionary-encoded.

## See

- Skill: `cohort-schema-design` (Field-type matrix), `grouper-design` (`GROUP_DATE`).
- Cross-link: `type-date` for day resolution, `type-u64` for sub-second timestamps.
