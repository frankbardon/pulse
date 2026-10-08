---
name: op-attr-date-part
description: Extract a calendar component (year, month, day, year_month, ..., hour) from a date or datetime field.
kind: operator
category: ATTR
operator: ATTR_DATE_PART
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, feature-engineering, streaming-friendly]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

- `part` — enum, required: `day`, `hour`, `month`, `month_day`, `year`, `year_month`, `year_month_day`.

## Inputs

`Field` — `date` or `datetime` (a schema field). `Label` — required, new column name.

## Output

One `f64` integer per record: `year` = YYYY, `month` = 1..12, `day` = 1..31, `year_month` = YYYYMM, `year_month_day` = YYYYMMDD, `month_day` = MMDD, `hour` = 0..23. Null source → `0` (not null).

## Gotchas

- Row-local one-pass — streams cleanly.
- Useful as a grouping key (e.g. `month_day` for seasonality) or as a `FEAT` substitute when post-filter visibility is needed.
- Unknown `part`, non-date-family field, `hour` on a `date` → `PROCESSING_CONFIG`.
- Zone-capable: a `datetime` reads the LOCAL clock (slot `tz` → `time_zone` → default → UTC). A `date` ignores zones: explicit `tz` refused, inherited not applied.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: `attribute-composition`, `op-attr-formula`, `request-envelope` (Time zones)
