```yaml
name: type-date
kind: type
description: Day-granularity date stored as epoch days (signed since 1970-01-01).
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 4 bytes per record, little-endian, a two's-complement **signed int32** count of days since 1970-01-01 (read a raw `ReadFieldValue` word with `encoding.DateDays`). Not bit-packed. **Not interchangeable with `datetime`** (8 bytes of epoch SECONDS).

## Range

Negative counts are pre-1970 dates (1969-12-31 is −1, 1900-01-01 is −25567); ordering, filters, groupers, min / max, lookup keys and every export adapter honour the sign. Import parses the `encoding.DateFormats` layouts, first match wins (`01/02/2006` reads month first); a literal with a time of day infers `datetime` instead. No time of day is stored: use `datetime`. **Output form:** a row cell (sample, lookup, record rows, a lag / lead window copy) is `YYYY-MM-DD`; aggregates over the day count stay numbers.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. `0` is 1970-01-01, never null. `IsNumericForAnalytics()` is `true`, so aggregators read the day count directly and skip nulls; `IsNumeric()` is `false`. No smart-default aggregator; the smart-default grouper is `GROUP_DATE` (`component: day`).

## Dictionary

Absent. Dates are never dictionary-encoded.

## See

- Skills: [`cohort-schema-design`](cohort-schema-design.md) (Field-type matrix), [`grouper-design`](grouper-design.md), [`time-zones`](time-zones.md).
- Cross-link: [`type-datetime`](type-datetime.md).
