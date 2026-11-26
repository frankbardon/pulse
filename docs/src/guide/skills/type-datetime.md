```yaml
name: type-datetime
kind: type
description: Second-granularity instant stored as signed epoch seconds (int64, negative = pre-1970), naive UTC.
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 8 bytes per record, little-endian, a two's-complement **signed int64** count of whole SECONDS since 1970-01-01T00:00:00Z (read a raw `ReadFieldValue` word with `encoding.DateTimeSeconds`). Not bit-packed. **Not interchangeable with `date`** (4 bytes of epoch DAYS): swapping the two rescales every value by 86,400.

## Range

The full signed `int64` second range: pre-1970 instants round-trip through `encoding.ParseDateTime` / `FormatDateTime` and order, filter, aggregate and export correctly across the epoch. Canonical text `2006-01-02T15:04:05Z` (`encoding.CanonicalDateTimeLayout`); accepted inputs `encoding.DateTimeFormats`; ambiguous slash forms (`03/04/2024`) and date-only literals are `ENCODING_INVALID` (a date-only column infers `date`). Row cells render the text form; aggregates stay numbers.

Resolution is **seconds** and the stored instant is **naive UTC** (an offset literal keeps its instant, not its offset). Sub-second flooring, offsets, export zones and day truncation: [`time-zones`](time-zones.md) (Stored instants).

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. `0` is the epoch instant, never null. `IsNumericForAnalytics()` is `true` (the second count aggregates and regresses directly, nulls skipped); `IsNumeric()` is `false`. Index-keyable: key literals parse through `ParseDateTime`, never as floats.

## Dictionary

Absent. Datetimes are never dictionary-encoded.

## See

- Skills: [`cohort-schema-design`](cohort-schema-design.md) (Field-type matrix), [`grouper-design`](grouper-design.md), [`time-zones`](time-zones.md) (local days, import and export zones).
- Cross-link: [`type-date`](type-date.md) (day resolution), [`type-u64`](type-u64.md) (sub-second timestamps as microseconds).
