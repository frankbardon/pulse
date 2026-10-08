```yaml
name: op-group-date
description: Partition date/datetime records by calendar component (hour..year); week_start picks the week day; fiscal_offset shifts year/quarter.
kind: operator
category: GROUP
operator: GROUP_DATE
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, streaming-friendly]
```

## Use when

Splits the rows by calendar period of a date, such as month, quarter, ISO week or weekday, to see a measure over time.

Questions it answers:

- How many orders came in each month?
- Which day of the week gets the most complaints?

Use something else:

- `GROUP_DATE_RANGES` when your periods are custom, such as campaign windows.
- `OVERLAY_INDEX_VS_PRIOR` when you want each period against the one before it.

## Params

- `component`: enum, default `month`: `hour`, `day`, `day_of_week`, `week`, `month`, `quarter`, `year`.
- `week_start`: `week` only; `monday` (default, ISO `2026-W09`) .. `sunday` (key = first day: 2026-03-01).
- `fiscal_offset`: int -11..11; `year`/`quarter` only; keys `FY` (end-year).
- `tz`: slot key (IANA), beats `time_zone`.

## Inputs

`Field`: `date`, `datetime` (in the resolved zone). `hour`: `datetime` only.

## Output

String key (`2024-Q1`, `FY2025-Q1`, `2026-03-29T03`). Smart default (date-family).

## Components

Floor `{total_n, n_null}` + `granularity`, `range_start` / `range_end`, `n_buckets`, `buckets` (`{key, period_start, period_end, count}`; `hour` period = label). `Mergeable`; `Streamable=false` (stream: `GROUP_CATEGORY` over `ATTR_DATE_PART`).

## Gotchas

- `day_of_week` lex-sorts; `Group.Include` ignored.
- Local wall clock: a skipped DST hour has no bucket; a repeated one is ONE.
- `PROCESSING_CONFIG`: `tz` on `date`, zone on a derived field, `hour` off `datetime`, misplaced `week_start` / `fiscal_offset`.

## See

- `pulse_examples_search tags=[time-series]`
- Skills: [`grouper-design`](grouper-design.md), [`response-components`](response-components.md), [`request-envelope`](request-envelope.md) (Time zones)
