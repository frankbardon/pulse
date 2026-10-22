```yaml
name: op-attr-date-part
description: Extract a calendar component (year, month, day, year_month, ..., hour, iso_weekday) from a date or datetime field.
kind: operator
category: ATTR
operator: ATTR_DATE_PART
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, feature-engineering, streaming-friendly]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds a calendar part of a date or timestamp to every row, such as the year, the month, a year-month like 202403 or the hour.

Questions it answers:

- Which month of the year does each order fall in, so seasons can be compared across years?
- What year-month label should each response carry for a monthly breakdown?

Use something else:

- `GROUP_DATE` when you only want rows grouped by day, week, month or year.

## Params

- `part` — enum, required: `day`, `hour`, `iso_weekday`, `month`, `month_day`, `year`, `year_month`, `year_month_day`.

## Inputs

`Field` — `date` or `datetime` (a schema field). `Label` — required, new column name.

## Output

One `f64` integer per record: `year` = YYYY, `month` = 1..12, `day` = 1..31, `year_month` = YYYYMM, `year_month_day` = YYYYMMDD, `month_day` = MMDD, `hour` = 0..23, `iso_weekday` = 1..7 (ISO: 1 = Monday, 7 = Sunday; the LOCAL day on a `datetime`). Null `date` / `datetime` source → `null` (never `0`), counted in the aggregation's `n_null`.

## Contract

- Types (`field`): `date`, `datetime`; any other scalar type is `PULSE_FIELD_TYPE_UNSUPPORTED`.
- Weight: not read; a weight in force is skipped.
- Streaming: streamable.

## Gotchas

- Row-local one-pass — streams cleanly.
- A grouping key (`month_day` for seasonality, `iso_weekday` sorts Monday first) or a post-filter `FEAT` substitute.
- `hour` on a `date` → `PULSE_FIELD_TYPE_UNSUPPORTED`; unknown `part` → `PROCESSING_CONFIG`.
- Zone-capable: a `datetime` reads the LOCAL clock (slot `tz` / `tz_field` → `time_zone_field` / `time_zone` → default → UTC); null `tz_field` cell → `null`. A `date` ignores zones (explicit refused).

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`attribute-composition`](attribute-composition.md), [`op-attr-formula`](op-attr-formula.md), [`request-envelope`](request-envelope.md) (Time zones)
